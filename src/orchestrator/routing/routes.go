package routing

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"eve-beemo/src/orchestrator/embedding"
	"gopkg.in/yaml.v3"
)

type Domain struct {
	ID              string   `yaml:"id"`
	Title           string   `yaml:"title"`
	Summary         string   `yaml:"summary"`
	WhenToUse       []string `yaml:"when_to_use"`
	WhenNotToUse    []string `yaml:"when_not_to_use"`
	ExampleRequests []string `yaml:"example_requests"`
}

type Handler struct {
	Type   string `yaml:"type"`
	Target string `yaml:"target"`
}

type MemoryPolicy struct {
	Read  bool     `yaml:"read"`
	Write bool     `yaml:"write"`
	Attrs []string `yaml:"attrs"`
	Scope string   `yaml:"scope"`
}

type Route struct {
	ID              string         `yaml:"id"`
	Domain          string         `yaml:"domain"`
	ParentRoute     string         `yaml:"parent_route"`
	Title           string         `yaml:"title"`
	Summary         string         `yaml:"summary"`
	WhenToUse       []string       `yaml:"when_to_use"`
	WhenNotToUse    []string       `yaml:"when_not_to_use"`
	ExampleRequests []string       `yaml:"example_requests"`
	RequiredFields  []string       `yaml:"required_fields"`
	ArgsGuidance    string         `yaml:"args_guidance"`
	DefaultArgs     map[string]any `yaml:"default_args"`
	Handler         Handler        `yaml:"handler"`
	Memory          MemoryPolicy   `yaml:"memory"`
}

type Catalog struct {
	Domains []Domain `yaml:"domains"`
	Routes  []Route  `yaml:"routes"`
}

type Candidate struct {
	Route  Route
	Domain Domain
	Score  float64
}

type domainCandidate struct {
	Domain Domain
	Score  float64
}

type routeDocument struct {
	RouteIndex int
	Text       string
}

type domainDocument struct {
	DomainIndex int
	Text        string
}

type Selector struct {
	path       string
	httpURL    string
	model      string
	topK       int
	domainTopK int
	db         *sql.DB
	embedFn    func(httpURL, model string, inputs []string, timeout time.Duration) ([][]float32, error)

	mu               sync.Mutex
	contentSig       [32]byte
	domains          []Domain
	domainDocuments  []domainDocument
	domainEmbeddings [][]float32
	routes           []Route
	routeDocuments   []routeDocument
	routeEmbeddings  [][]float32
}

func NewSelector(path, httpURL, model string, topK, domainTopK int) *Selector {
	if topK <= 0 {
		topK = 5
	}
	if domainTopK <= 0 {
		domainTopK = 2
	}
	return &Selector{
		path:       strings.TrimSpace(path),
		httpURL:    strings.TrimSpace(httpURL),
		model:      strings.TrimSpace(model),
		topK:       topK,
		domainTopK: domainTopK,
		embedFn:    embedding.Call,
	}
}

func (s *Selector) Enabled() bool {
	return s != nil && s.path != "" && s.httpURL != ""
}

func (s *Selector) Warmup(timeout time.Duration) error {
	if !s.Enabled() {
		return nil
	}

	if _, err := s.embedFn(s.httpURL, s.model, []string{"startup probe"}, timeout); err != nil {
		return fmt.Errorf("embedding startup probe failed: %w", err)
	}

	_, _, _, _, _, _, err := s.ensureLoaded(timeout)
	if err != nil {
		return fmt.Errorf("route warmup failed: %w", err)
	}
	return nil
}

func (s *Selector) Retrieve(query string, timeout time.Duration) ([]Candidate, error) {
	if !s.Enabled() {
		return nil, nil
	}
	trimmed := strings.TrimSpace(query)
	if trimmed == "" {
		return nil, nil
	}

	domains, domainDocs, domainEmbeddings, routes, routeDocs, routeEmbeddings, err := s.ensureLoaded(timeout)
	if err != nil {
		return nil, err
	}
	if len(routes) == 0 || len(routeDocs) == 0 || len(routeEmbeddings) == 0 {
		return nil, nil
	}

	queryEmbeddings, err := s.embedFn(s.httpURL, s.model, []string{queryInstruction(trimmed)}, timeout)
	if err != nil {
		return nil, err
	}
	if len(queryEmbeddings) == 0 || len(queryEmbeddings[0]) == 0 {
		return nil, fmt.Errorf("query embedding response: no data")
	}
	queryEmbedding := queryEmbeddings[0]

	allowedDomains := rankedDomainIDs(queryEmbedding, domains, domainDocs, domainEmbeddings, s.domainTopK)
	candidates := rankRoutes(queryEmbedding, domains, routes, routeDocs, routeEmbeddings, allowedDomains)
	if len(candidates) > s.topK {
		candidates = candidates[:s.topK]
	}
	return candidates, nil
}

func FormatCandidates(candidates []Candidate) string {
	if len(candidates) == 0 {
		return "(none)"
	}

	var b strings.Builder
	for i, candidate := range candidates {
		route := candidate.Route
		domain := candidate.Domain
		fmt.Fprintf(&b, "Candidate %d\n", i+1)
		fmt.Fprintf(&b, "- route_id: %s\n", route.ID)
		fmt.Fprintf(&b, "- score: %.4f\n", candidate.Score)
		if domain.ID != "" {
			fmt.Fprintf(&b, "- domain_id: %s\n", domain.ID)
		}
		if domain.Title != "" {
			fmt.Fprintf(&b, "- domain_title: %s\n", domain.Title)
		}
		if route.ParentRoute != "" {
			fmt.Fprintf(&b, "- parent_route: %s\n", route.ParentRoute)
		}
		fmt.Fprintf(&b, "- title: %s\n", route.Title)
		fmt.Fprintf(&b, "- handler_type: %s\n", route.Handler.Type)
		fmt.Fprintf(&b, "- handler_target: %s\n", route.Handler.Target)
		if len(route.DefaultArgs) > 0 {
			if raw, err := json.Marshal(route.DefaultArgs); err == nil {
				fmt.Fprintf(&b, "- default_args: %s\n", raw)
			}
		}
		if route.Memory.Read || route.Memory.Write || len(route.Memory.Attrs) > 0 || route.Memory.Scope != "" {
			fmt.Fprintf(&b, "- memory_read: %t\n", route.Memory.Read)
			fmt.Fprintf(&b, "- memory_write: %t\n", route.Memory.Write)
			if len(route.Memory.Attrs) > 0 {
				fmt.Fprintf(&b, "- memory_attrs: %s\n", strings.Join(route.Memory.Attrs, ", "))
			}
			if route.Memory.Scope != "" {
				fmt.Fprintf(&b, "- memory_scope: %s\n", route.Memory.Scope)
			}
		}
		if summary := strings.TrimSpace(route.Summary); summary != "" {
			fmt.Fprintf(&b, "- summary: %s\n", summary)
		}
		if len(route.RequiredFields) > 0 {
			fmt.Fprintf(&b, "- required_fields: %s\n", strings.Join(route.RequiredFields, ", "))
		}
		if guidance := strings.TrimSpace(route.ArgsGuidance); guidance != "" {
			fmt.Fprintf(&b, "- args_guidance: %s\n", guidance)
		}
		if len(route.WhenToUse) > 0 {
			fmt.Fprintf(&b, "- when_to_use:\n")
			for _, item := range route.WhenToUse {
				fmt.Fprintf(&b, "  - %s\n", item)
			}
		}
		if len(route.WhenNotToUse) > 0 {
			fmt.Fprintf(&b, "- when_not_to_use:\n")
			for _, item := range route.WhenNotToUse {
				fmt.Fprintf(&b, "  - %s\n", item)
			}
		}
		if len(route.ExampleRequests) > 0 {
			fmt.Fprintf(&b, "- example_requests:\n")
			for _, item := range route.ExampleRequests {
				fmt.Fprintf(&b, "  - %s\n", item)
			}
		}
	}

	return strings.TrimSpace(b.String())
}

func queryInstruction(query string) string {
	return "Instruct: Given a user request, retrieve the best assistant execution pathway.\nQuery: " + query
}

func (s *Selector) Routes() []Route {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneRoutes(s.routes)
}

func MatchCall(candidates []Candidate, routes []Route, action string, args json.RawMessage) (Route, bool, error) {
	action = strings.TrimSpace(action)
	if action == "" {
		return Route{}, false, nil
	}

	argsMap := map[string]any{}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &argsMap); err != nil {
			return Route{}, false, fmt.Errorf("invalid tool args for route match: %w", err)
		}
	}

	if route, ok := bestRouteMatch(candidateRoutes(candidates), action, argsMap); ok {
		return route, true, nil
	}
	if route, ok := bestRouteMatch(routes, action, argsMap); ok {
		return route, true, nil
	}
	if route, ok := inferRouteForCall(action, argsMap); ok {
		return route, true, nil
	}
	return Route{}, false, nil
}

func candidateRoutes(candidates []Candidate) []Route {
	if len(candidates) == 0 {
		return nil
	}
	routes := make([]Route, 0, len(candidates))
	for _, candidate := range candidates {
		routes = append(routes, candidate.Route)
	}
	return routes
}

func bestRouteMatch(routes []Route, action string, args map[string]any) (Route, bool) {
	bestIndex := -1
	bestScore := -1
	for i, route := range routes {
		if strings.TrimSpace(route.Handler.Type) != "tool" || strings.TrimSpace(route.Handler.Target) != action {
			continue
		}
		if !matchesDefaultArgs(route.DefaultArgs, args) {
			continue
		}
		score := len(route.DefaultArgs)
		if bestIndex < 0 || score > bestScore {
			bestIndex = i
			bestScore = score
		}
	}
	if bestIndex < 0 {
		return Route{}, false
	}
	return routes[bestIndex], true
}

func matchesDefaultArgs(defaultArgs map[string]any, args map[string]any) bool {
	if len(defaultArgs) == 0 {
		return true
	}
	for key, want := range defaultArgs {
		got, ok := args[key]
		if !ok {
			return false
		}
		if !jsonValuesEqual(got, want) {
			return false
		}
	}
	return true
}

func jsonValuesEqual(a, b any) bool {
	rawA, errA := json.Marshal(a)
	rawB, errB := json.Marshal(b)
	if errA != nil || errB != nil {
		return false
	}
	return string(rawA) == string(rawB)
}

func inferRouteForCall(action string, args map[string]any) (Route, bool) {
	switch action {
	case "get_time":
		return Route{
			ID:      "get_time.current_or_relative",
			Domain:  "time",
			Handler: Handler{Type: "tool", Target: "get_time"},
		}, true
	case "calculator":
		operation := strings.TrimSpace(stringField(args["operation"]))
		switch operation {
		case "bmi":
			return syntheticCalculatorRoute("calculator.bmi", operation, []string{"weight", "height"}), true
		case "bmr":
			return syntheticCalculatorRoute("calculator.bmr", operation, []string{"weight", "height", "age_years", "gender"}), true
		case "tdee":
			return syntheticCalculatorRoute("calculator.tdee", operation, []string{"weight", "height", "age_years", "gender", "activity_level"}), true
		case "expression":
			return Route{ID: "calculator.expression", Domain: "calculator", Handler: Handler{Type: "tool", Target: "calculator"}, DefaultArgs: map[string]any{"operation": operation}}, true
		case "convert":
			return Route{ID: "calculator.convert", Domain: "calculator", Handler: Handler{Type: "tool", Target: "calculator"}, DefaultArgs: map[string]any{"operation": operation}}, true
		case "percent_of":
			return Route{ID: "calculator.percent_of", Domain: "calculator", Handler: Handler{Type: "tool", Target: "calculator"}, DefaultArgs: map[string]any{"operation": operation}}, true
		case "percent_change":
			return Route{ID: "calculator.percent_change", Domain: "calculator", Handler: Handler{Type: "tool", Target: "calculator"}, DefaultArgs: map[string]any{"operation": operation}}, true
		case "percent_ratio":
			return Route{ID: "calculator.percent_ratio", Domain: "calculator", Handler: Handler{Type: "tool", Target: "calculator"}, DefaultArgs: map[string]any{"operation": operation}}, true
		}
	}
	return Route{}, false
}

func syntheticCalculatorRoute(id, operation string, attrs []string) Route {
	return Route{
		ID:         id,
		Domain:     "calculator",
		DefaultArgs: map[string]any{"operation": operation},
		Handler:    Handler{Type: "tool", Target: "calculator"},
		Memory: MemoryPolicy{
			Read:  true,
			Write: true,
			Attrs: append([]string(nil), attrs...),
			Scope: "subject",
		},
	}
}

func stringField(v any) string {
	s, _ := v.(string)
	return s
}

func (s *Selector) ensureLoaded(timeout time.Duration) ([]Domain, []domainDocument, [][]float32, []Route, []routeDocument, [][]float32, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return nil, nil, nil, nil, nil, nil, err
	}
	sig := sha256.Sum256(data)

	s.mu.Lock()
	defer s.mu.Unlock()
	if sig == s.contentSig &&
		len(s.routes) > 0 &&
		len(s.routeDocuments) > 0 &&
		len(s.routeEmbeddings) == len(s.routeDocuments) &&
		len(s.domains) > 0 &&
		len(s.domainDocuments) > 0 &&
		len(s.domainEmbeddings) == len(s.domainDocuments) {
		return cloneDomains(s.domains), cloneDomainDocuments(s.domainDocuments), cloneEmbeddings(s.domainEmbeddings), cloneRoutes(s.routes), cloneRouteDocuments(s.routeDocuments), cloneEmbeddings(s.routeEmbeddings), nil
	}

	var catalog Catalog
	if err := yaml.Unmarshal(data, &catalog); err != nil {
		return nil, nil, nil, nil, nil, nil, err
	}

	routes := normalizeRoutes(catalog.Routes)
	if len(routes) == 0 {
		return nil, nil, nil, nil, nil, nil, fmt.Errorf("routes catalog is empty")
	}

	domains := normalizeDomains(catalog.Domains, routes)
	routes = assignRouteDomains(routes, domains)
	if err := s.syncRouteDefinitions(routes, timeout); err != nil {
		return nil, nil, nil, nil, nil, nil, err
	}
	domainDocuments := buildDomainDocuments(domains)
	routeDocuments := buildRouteDocuments(routes)

	domainEmbeddings, err := embedDocuments(s.embedFn, s.httpURL, s.model, domainDocumentsToInputs(domainDocuments), timeout)
	if err != nil {
		return nil, nil, nil, nil, nil, nil, err
	}
	if len(domainEmbeddings) != len(domainDocuments) {
		return nil, nil, nil, nil, nil, nil, fmt.Errorf("domain embeddings mismatch: got %d want %d", len(domainEmbeddings), len(domainDocuments))
	}

	routeEmbeddings, err := embedDocuments(s.embedFn, s.httpURL, s.model, routeDocumentsToInputs(routeDocuments), timeout)
	if err != nil {
		return nil, nil, nil, nil, nil, nil, err
	}
	if len(routeEmbeddings) != len(routeDocuments) {
		return nil, nil, nil, nil, nil, nil, fmt.Errorf("route embeddings mismatch: got %d want %d", len(routeEmbeddings), len(routeDocuments))
	}

	s.contentSig = sig
	s.domains = cloneDomains(domains)
	s.domainDocuments = cloneDomainDocuments(domainDocuments)
	s.domainEmbeddings = cloneEmbeddings(domainEmbeddings)
	s.routes = cloneRoutes(routes)
	s.routeDocuments = cloneRouteDocuments(routeDocuments)
	s.routeEmbeddings = cloneEmbeddings(routeEmbeddings)

	return cloneDomains(s.domains), cloneDomainDocuments(s.domainDocuments), cloneEmbeddings(s.domainEmbeddings), cloneRoutes(s.routes), cloneRouteDocuments(s.routeDocuments), cloneEmbeddings(s.routeEmbeddings), nil
}

func normalizeDomains(domains []Domain, routes []Route) []Domain {
	normalized := make([]Domain, 0, len(domains))
	seen := make(map[string]struct{})
	for _, domain := range domains {
		domain.ID = strings.TrimSpace(domain.ID)
		domain.Title = strings.TrimSpace(domain.Title)
		domain.Summary = strings.TrimSpace(domain.Summary)
		if domain.ID == "" {
			continue
		}
		if _, ok := seen[domain.ID]; ok {
			continue
		}
		seen[domain.ID] = struct{}{}
		normalized = append(normalized, domain)
	}

	for _, route := range routes {
		domainID := inferredDomainID(route)
		if domainID == "" {
			continue
		}
		if _, ok := seen[domainID]; ok {
			continue
		}
		seen[domainID] = struct{}{}
		normalized = append(normalized, Domain{
			ID:      domainID,
			Title:   titleFromIdentifier(domainID),
			Summary: "Synthetic domain inferred from routes.",
		})
	}
	return normalized
}

func normalizeRoutes(routes []Route) []Route {
	normalized := make([]Route, 0, len(routes))
	for _, route := range routes {
		route.ID = strings.TrimSpace(route.ID)
		route.Domain = strings.TrimSpace(route.Domain)
		route.ParentRoute = strings.TrimSpace(route.ParentRoute)
		route.Title = strings.TrimSpace(route.Title)
		route.Summary = strings.TrimSpace(route.Summary)
		route.ArgsGuidance = strings.TrimSpace(route.ArgsGuidance)
		route.Handler.Type = strings.TrimSpace(route.Handler.Type)
		route.Handler.Target = strings.TrimSpace(route.Handler.Target)
		route.Memory.Scope = strings.TrimSpace(route.Memory.Scope)
		route.Memory.Attrs = normalizeAttrs(route.Memory.Attrs)
		if route.ID == "" || route.Handler.Type == "" || route.Handler.Target == "" {
			continue
		}
		if route.DefaultArgs == nil {
			route.DefaultArgs = map[string]any{}
		}
		normalized = append(normalized, route)
	}
	return normalized
}

func assignRouteDomains(routes []Route, domains []Domain) []Route {
	known := make(map[string]struct{}, len(domains))
	for _, domain := range domains {
		known[domain.ID] = struct{}{}
	}

	out := make([]Route, 0, len(routes))
	for _, route := range routes {
		route.Domain = strings.TrimSpace(route.Domain)
		if route.Domain == "" {
			route.Domain = inferredDomainID(route)
		}
		if route.Domain == "" {
			route.Domain = "general"
		}
		if _, ok := known[route.Domain]; !ok {
			known[route.Domain] = struct{}{}
		}
		out = append(out, route)
	}
	return out
}

func buildDomainDocuments(domains []Domain) []domainDocument {
	docs := make([]domainDocument, 0, len(domains)*2)
	for i, domain := range domains {
		docs = append(docs, domainDocument{
			DomainIndex: i,
			Text:        domainDescriptor(domain),
		})
		for _, example := range domain.ExampleRequests {
			example = strings.TrimSpace(example)
			if example == "" {
				continue
			}
			docs = append(docs, domainDocument{
				DomainIndex: i,
				Text: fmt.Sprintf(
					"Domain: %s\nTitle: %s\nSummary: %s\nExample request: %s",
					domain.ID,
					domain.Title,
					domain.Summary,
					example,
				),
			})
		}
	}
	return docs
}

func buildRouteDocuments(routes []Route) []routeDocument {
	docs := make([]routeDocument, 0, len(routes)*2)
	for i, route := range routes {
		docs = append(docs, routeDocument{
			RouteIndex: i,
			Text:       routeDescriptor(route),
		})
		for _, example := range route.ExampleRequests {
			example = strings.TrimSpace(example)
			if example == "" {
				continue
			}
			docs = append(docs, routeDocument{
				RouteIndex: i,
				Text: fmt.Sprintf(
					"Route: %s\nDomain: %s\nTitle: %s\nSummary: %s\nExample request: %s",
					route.ID,
					route.Domain,
					route.Title,
					route.Summary,
					example,
				),
			})
		}
	}
	return docs
}

func domainDescriptor(domain Domain) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Domain: %s\n", domain.ID)
	if domain.Title != "" {
		fmt.Fprintf(&b, "Title: %s\n", domain.Title)
	}
	if domain.Summary != "" {
		fmt.Fprintf(&b, "Summary: %s\n", domain.Summary)
	}
	if len(domain.WhenToUse) > 0 {
		fmt.Fprintf(&b, "When to use: %s\n", strings.Join(domain.WhenToUse, " | "))
	}
	if len(domain.WhenNotToUse) > 0 {
		fmt.Fprintf(&b, "When not to use: %s\n", strings.Join(domain.WhenNotToUse, " | "))
	}
	if len(domain.ExampleRequests) > 0 {
		fmt.Fprintf(&b, "Example requests: %s\n", strings.Join(domain.ExampleRequests, " | "))
	}
	return strings.TrimSpace(b.String())
}

func routeDescriptor(route Route) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Route: %s\n", route.ID)
	if route.Domain != "" {
		fmt.Fprintf(&b, "Domain: %s\n", route.Domain)
	}
	if route.ParentRoute != "" {
		fmt.Fprintf(&b, "Parent route: %s\n", route.ParentRoute)
	}
	if route.Title != "" {
		fmt.Fprintf(&b, "Title: %s\n", route.Title)
	}
	if route.Summary != "" {
		fmt.Fprintf(&b, "Summary: %s\n", route.Summary)
	}
	fmt.Fprintf(&b, "Handler type: %s\n", route.Handler.Type)
	fmt.Fprintf(&b, "Handler target: %s\n", route.Handler.Target)
	if len(route.DefaultArgs) > 0 {
		if raw, err := json.Marshal(route.DefaultArgs); err == nil {
			fmt.Fprintf(&b, "Default args: %s\n", raw)
		}
	}
	if route.Memory.Read || route.Memory.Write || len(route.Memory.Attrs) > 0 || route.Memory.Scope != "" {
		fmt.Fprintf(&b, "Memory read: %t\n", route.Memory.Read)
		fmt.Fprintf(&b, "Memory write: %t\n", route.Memory.Write)
		if len(route.Memory.Attrs) > 0 {
			fmt.Fprintf(&b, "Memory attrs: %s\n", strings.Join(route.Memory.Attrs, ", "))
		}
		if route.Memory.Scope != "" {
			fmt.Fprintf(&b, "Memory scope: %s\n", route.Memory.Scope)
		}
	}
	if route.ArgsGuidance != "" {
		fmt.Fprintf(&b, "Args guidance: %s\n", route.ArgsGuidance)
	}
	if len(route.RequiredFields) > 0 {
		fmt.Fprintf(&b, "Required fields: %s\n", strings.Join(route.RequiredFields, ", "))
	}
	if len(route.WhenToUse) > 0 {
		fmt.Fprintf(&b, "When to use: %s\n", strings.Join(route.WhenToUse, " | "))
	}
	if len(route.WhenNotToUse) > 0 {
		fmt.Fprintf(&b, "When not to use: %s\n", strings.Join(route.WhenNotToUse, " | "))
	}
	if len(route.ExampleRequests) > 0 {
		fmt.Fprintf(&b, "Example requests: %s\n", strings.Join(route.ExampleRequests, " | "))
	}
	return strings.TrimSpace(b.String())
}

func rankedDomainIDs(queryEmbedding []float32, domains []Domain, docs []domainDocument, embeddings [][]float32, topK int) map[string]struct{} {
	if len(domains) == 0 || len(docs) == 0 || len(embeddings) == 0 {
		return nil
	}

	bestByDomain := make(map[int]float64, len(domains))
	for i, doc := range docs {
		if i >= len(embeddings) {
			break
		}
		score := cosineSimilarity(queryEmbedding, embeddings[i])
		if prev, ok := bestByDomain[doc.DomainIndex]; !ok || score > prev {
			bestByDomain[doc.DomainIndex] = score
		}
	}

	ranked := make([]domainCandidate, 0, len(bestByDomain))
	for idx, score := range bestByDomain {
		if idx < 0 || idx >= len(domains) {
			continue
		}
		ranked = append(ranked, domainCandidate{
			Domain: domains[idx],
			Score:  score,
		})
	}

	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].Score == ranked[j].Score {
			return ranked[i].Domain.ID < ranked[j].Domain.ID
		}
		return ranked[i].Score > ranked[j].Score
	})

	if topK > 0 && len(ranked) > topK {
		ranked = ranked[:topK]
	}
	if len(ranked) == 0 {
		return nil
	}

	allowed := make(map[string]struct{}, len(ranked))
	for _, candidate := range ranked {
		allowed[candidate.Domain.ID] = struct{}{}
	}
	return allowed
}

func rankRoutes(queryEmbedding []float32, domains []Domain, routes []Route, docs []routeDocument, embeddings [][]float32, allowedDomains map[string]struct{}) []Candidate {
	domainByID := make(map[string]Domain, len(domains))
	for _, domain := range domains {
		domainByID[domain.ID] = domain
	}

	bestByRoute := make(map[int]float64, len(routes))
	for i, doc := range docs {
		if i >= len(embeddings) {
			break
		}
		routeIdx := doc.RouteIndex
		if routeIdx < 0 || routeIdx >= len(routes) {
			continue
		}
		route := routes[routeIdx]
		if len(allowedDomains) > 0 {
			if _, ok := allowedDomains[route.Domain]; !ok {
				continue
			}
		}
		score := cosineSimilarity(queryEmbedding, embeddings[i])
		if prev, ok := bestByRoute[routeIdx]; !ok || score > prev {
			bestByRoute[routeIdx] = score
		}
	}

	candidates := make([]Candidate, 0, len(bestByRoute))
	for idx, score := range bestByRoute {
		if idx < 0 || idx >= len(routes) {
			continue
		}
		route := routes[idx]
		candidates = append(candidates, Candidate{
			Route:  route,
			Domain: domainByID[route.Domain],
			Score:  score,
		})
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Score == candidates[j].Score {
			return candidates[i].Route.ID < candidates[j].Route.ID
		}
		return candidates[i].Score > candidates[j].Score
	})
	return candidates
}

func embedDocuments(
	embedFn func(httpURL, model string, inputs []string, timeout time.Duration) ([][]float32, error),
	httpURL, model string,
	inputs []string,
	timeout time.Duration,
) ([][]float32, error) {
	if len(inputs) == 0 {
		return [][]float32{}, nil
	}
	return embedFn(httpURL, model, inputs, timeout)
}

func domainDocumentsToInputs(documents []domainDocument) []string {
	inputs := make([]string, 0, len(documents))
	for _, doc := range documents {
		inputs = append(inputs, doc.Text)
	}
	return inputs
}

func routeDocumentsToInputs(documents []routeDocument) []string {
	inputs := make([]string, 0, len(documents))
	for _, doc := range documents {
		inputs = append(inputs, doc.Text)
	}
	return inputs
}

func inferredDomainID(route Route) string {
	if strings.TrimSpace(route.Domain) != "" {
		return strings.TrimSpace(route.Domain)
	}
	if idx := strings.Index(route.ID, "."); idx > 0 {
		return strings.TrimSpace(route.ID[:idx])
	}
	if strings.TrimSpace(route.Handler.Target) != "" {
		return strings.TrimSpace(route.Handler.Target)
	}
	return ""
}

func titleFromIdentifier(id string) string {
	if id == "" {
		return ""
	}
	parts := strings.FieldsFunc(id, func(r rune) bool {
		return r == '.' || r == '_' || r == '-'
	})
	for i, part := range parts {
		if part == "" {
			continue
		}
		parts[i] = strings.ToUpper(part[:1]) + part[1:]
	}
	return strings.Join(parts, " ")
}

func cosineSimilarity(a, b []float32) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	var dot float64
	var magA float64
	var magB float64
	for i := 0; i < n; i++ {
		av := float64(a[i])
		bv := float64(b[i])
		dot += av * bv
		magA += av * av
		magB += bv * bv
	}
	if magA == 0 || magB == 0 {
		return 0
	}
	return dot / (math.Sqrt(magA) * math.Sqrt(magB))
}

func cloneDomains(domains []Domain) []Domain {
	out := make([]Domain, 0, len(domains))
	for _, domain := range domains {
		cloned := domain
		cloned.WhenToUse = append([]string(nil), domain.WhenToUse...)
		cloned.WhenNotToUse = append([]string(nil), domain.WhenNotToUse...)
		cloned.ExampleRequests = append([]string(nil), domain.ExampleRequests...)
		out = append(out, cloned)
	}
	return out
}

func cloneRoutes(routes []Route) []Route {
	out := make([]Route, 0, len(routes))
	for _, route := range routes {
		cloned := route
		cloned.WhenToUse = append([]string(nil), route.WhenToUse...)
		cloned.WhenNotToUse = append([]string(nil), route.WhenNotToUse...)
		cloned.ExampleRequests = append([]string(nil), route.ExampleRequests...)
		cloned.RequiredFields = append([]string(nil), route.RequiredFields...)
		if route.DefaultArgs != nil {
			raw, _ := json.Marshal(route.DefaultArgs)
			var copied map[string]any
			_ = json.Unmarshal(raw, &copied)
			cloned.DefaultArgs = copied
		}
		cloned.Memory.Attrs = append([]string(nil), route.Memory.Attrs...)
		out = append(out, cloned)
	}
	return out
}

func normalizeAttrs(attrs []string) []string {
	if len(attrs) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(attrs))
	out := make([]string, 0, len(attrs))
	for _, attr := range attrs {
		attr = strings.TrimSpace(attr)
		if attr == "" {
			continue
		}
		if _, ok := seen[attr]; ok {
			continue
		}
		seen[attr] = struct{}{}
		out = append(out, attr)
	}
	return out
}

func cloneDomainDocuments(documents []domainDocument) []domainDocument {
	out := make([]domainDocument, len(documents))
	copy(out, documents)
	return out
}

func cloneRouteDocuments(documents []routeDocument) []routeDocument {
	out := make([]routeDocument, len(documents))
	copy(out, documents)
	return out
}

func cloneEmbeddings(vectors [][]float32) [][]float32 {
	out := make([][]float32, 0, len(vectors))
	for _, vector := range vectors {
		cloned := make([]float32, len(vector))
		copy(cloned, vector)
		out = append(out, cloned)
	}
	return out
}
