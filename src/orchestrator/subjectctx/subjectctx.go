package subjectctx

import (
	"fmt"
	"sort"
	"strings"

	pb "eve-beemo/proto/gen/proto"
)

const selfSubjectID = "self"

var (
	subjectLinkStopwords = map[string]struct{}{
		"a": {}, "about": {}, "an": {}, "and": {}, "at": {}, "for": {}, "from": {}, "has": {}, "have": {}, "he": {}, "her": {},
		"his": {}, "i": {}, "if": {}, "in": {}, "is": {}, "me": {}, "mine": {}, "my": {}, "of": {}, "she": {}, "that": {}, "the": {},
		"their": {}, "they": {}, "was": {}, "weighs": {}, "weighed": {}, "weighing": {},
		"bmi": {}, "bmr": {}, "tdee": {}, "weight": {}, "height": {},
		"what": {}, "who": {}, "with": {},
	}
	relationAliases = map[string]string{
		"brother":  "brother",
		"dad":      "father",
		"daughter": "daughter",
		"father":   "father",
		"friend":   "friend",
		"husband":  "husband",
		"mom":      "mother",
		"mother":   "mother",
		"partner":  "partner",
		"sister":   "sister",
		"son":      "son",
		"trainer":  "trainer",
		"wife":     "wife",
	}
	selfPronouns            = []string{" i ", " me ", " my ", " mine "}
	thirdPersonPronouns     = []string{" he ", " him ", " his ", " she ", " her ", " hers ", " they ", " them ", " their ", " theirs "}
	directSubjectConnectors = map[string]struct{}{
		"about": {},
		"for":   {},
		"of":    {},
	}
	healthSubjectKeywords = []string{
		" bmi ", " bmr ", " tdee ", " weight ", " height ", " kg ", " lb ", " lbs ", " cm ", " male ", " female ",
	}
)

type Subject struct {
	ID      string
	Aliases []string
}

type Context struct {
	CurrentSubjectID string
	Subjects         []Subject
}

func (c Context) Summary() string {
	if c.CurrentSubjectID == "" && len(c.Subjects) == 0 {
		return ""
	}

	lines := make([]string, 0, len(c.Subjects)+1)
	if c.CurrentSubjectID != "" {
		lines = append(lines, fmt.Sprintf("current_subject_id: %s", c.CurrentSubjectID))
	}
	for _, subject := range c.Subjects {
		lines = append(lines, fmt.Sprintf("- subject_id: %s aliases: %s", subject.ID, strings.Join(subject.Aliases, ", ")))
	}
	return strings.Join(lines, "\n")
}

func Resolve(messages []*pb.ChatMessage) Context {
	return ResolveWithSeed(messages, nil)
}

func ResolveWithSeed(messages []*pb.ChatMessage, seeded []Subject) Context {
	r := resolver{
		subjects:   map[string]*subjectState{},
		aliasToIDs: map[string]map[string]struct{}{},
	}
	for _, subject := range seeded {
		r.seedSubject(subject)
	}
	var latestUser string

	for _, message := range messages {
		if message == nil {
			continue
		}
		if strings.ToLower(strings.TrimSpace(message.GetRole())) != "user" {
			continue
		}
		text := normalizeForMatching(message.GetContent())
		if text == "" {
			continue
		}
		r.linkExplicitSubjects(text)
		r.linkDirectHealthSubjects(text)
		mentioned := r.uniqueMentionedIDs(text)
		for _, subjectID := range mentioned {
			if subjectID != selfSubjectID {
				r.lastNonSelfSubjectID = subjectID
			}
		}
		latestUser = text
	}

	currentSubjectID := r.inferCurrentSubject(latestUser)
	if currentSubjectID == selfSubjectID {
		r.ensureSelfSubject()
	}

	subjects := make([]Subject, 0, len(r.order))
	for _, subjectID := range r.order {
		state := r.subjects[subjectID]
		if state == nil {
			continue
		}
		aliases := append([]string(nil), state.Aliases...)
		sort.Strings(aliases)
		subjects = append(subjects, Subject{
			ID:      subjectID,
			Aliases: aliases,
		})
	}

	return Context{
		CurrentSubjectID: currentSubjectID,
		Subjects:         subjects,
	}
}

type resolver struct {
	subjects             map[string]*subjectState
	aliasToIDs           map[string]map[string]struct{}
	order                []string
	lastNonSelfSubjectID string
}

type subjectState struct {
	ID      string
	Aliases []string
}

func (r *resolver) seedSubject(subject Subject) {
	subjectID := strings.TrimSpace(subject.ID)
	if subjectID == "" {
		return
	}
	state, exists := r.subjects[subjectID]
	if !exists {
		state = &subjectState{ID: subjectID}
		r.subjects[subjectID] = state
		r.order = append(r.order, subjectID)
	}
	for _, alias := range subject.Aliases {
		r.addAlias(state, alias)
	}
}

func (r *resolver) linkExplicitSubjects(text string) {
	words := strings.Fields(text)
	for idx := 0; idx < len(words); idx++ {
		word := words[idx]
		if word == "my" && idx+2 < len(words) {
			relation, ok := normalizeRelation(words[idx+1])
			if !ok {
				continue
			}
			if name, ok := extractName(words[idx+2:]); ok {
				r.registerLinkedSubject(name, relation)
			}
			continue
		}
		if idx+3 < len(words) && words[idx+1] == "is" && words[idx+2] == "my" {
			relation, ok := normalizeRelation(words[idx+3])
			if !ok {
				continue
			}
			if name, ok := extractName(words[idx:]); ok {
				r.registerLinkedSubject(name, relation)
			}
		}
	}
}

func (r *resolver) registerLinkedSubject(name, relation string) {
	subjectID := subjectIDForName(name)
	state, exists := r.subjects[subjectID]
	if !exists {
		state = &subjectState{ID: subjectID}
		r.subjects[subjectID] = state
		r.order = append(r.order, subjectID)
	}
	r.addAlias(state, name)
	r.addAlias(state, relation)
	r.addAlias(state, "my "+relation)
}

func (r *resolver) registerNamedSubject(name string) {
	subjectID := subjectIDForName(name)
	state, exists := r.subjects[subjectID]
	if !exists {
		state = &subjectState{ID: subjectID}
		r.subjects[subjectID] = state
		r.order = append(r.order, subjectID)
	}
	r.addAlias(state, name)
}

func (r *resolver) addAlias(state *subjectState, alias string) {
	normalized := normalizeForMatching(alias)
	if normalized == "" {
		return
	}
	for _, existing := range state.Aliases {
		if existing == normalized {
			goto aliasIndex
		}
	}
	state.Aliases = append(state.Aliases, normalized)

aliasIndex:
	if r.aliasToIDs[normalized] == nil {
		r.aliasToIDs[normalized] = map[string]struct{}{}
	}
	r.aliasToIDs[normalized][state.ID] = struct{}{}
}

func (r *resolver) uniqueMentionedIDs(text string) []string {
	matches := map[string]struct{}{}
	for alias, subjectIDs := range r.aliasToIDs {
		if !containsAlias(text, alias) {
			continue
		}
		if len(subjectIDs) != 1 {
			continue
		}
		for subjectID := range subjectIDs {
			matches[subjectID] = struct{}{}
		}
	}
	if len(matches) == 0 {
		return nil
	}
	ids := make([]string, 0, len(matches))
	for subjectID := range matches {
		ids = append(ids, subjectID)
	}
	sort.Strings(ids)
	return ids
}

func (r *resolver) linkDirectHealthSubjects(text string) {
	if !looksLikeHealthSubjectText(text) {
		return
	}
	words := strings.Fields(text)
	for idx, word := range words {
		if _, ok := directSubjectConnectors[word]; !ok {
			continue
		}
		if idx+1 >= len(words) {
			continue
		}
		if name, ok := extractName(words[idx+1:]); ok {
			r.registerNamedSubject(name)
		}
	}
	for idx, word := range words {
		if !isHealthKeyword(word) || idx == 0 {
			continue
		}
		for start := max(0, idx-2); start < idx; start++ {
			if name, ok := extractName(words[start:idx]); ok {
				r.registerNamedSubject(name)
				break
			}
		}
	}
}

func (r *resolver) inferCurrentSubject(text string) string {
	if text == "" {
		return ""
	}
	mentioned := r.uniqueMentionedIDs(text)
	if len(mentioned) == 1 {
		return mentioned[0]
	}
	if len(mentioned) > 1 {
		return ""
	}
	if r.hasAmbiguousAliasMention(text) {
		return ""
	}
	if containsAnyAlias(text, thirdPersonPronouns) && r.lastNonSelfSubjectID != "" {
		return r.lastNonSelfSubjectID
	}
	if containsAnyAlias(text, selfPronouns) {
		return selfSubjectID
	}
	return ""
}

func (r *resolver) hasAmbiguousAliasMention(text string) bool {
	for alias, subjectIDs := range r.aliasToIDs {
		if len(subjectIDs) <= 1 {
			continue
		}
		if containsAlias(text, alias) {
			return true
		}
	}
	return false
}

func (r *resolver) ensureSelfSubject() {
	if _, exists := r.subjects[selfSubjectID]; exists {
		return
	}
	state := &subjectState{ID: selfSubjectID}
	r.subjects[selfSubjectID] = state
	r.order = append(r.order, selfSubjectID)
	for _, alias := range []string{"i", "me", "my", "mine"} {
		r.addAlias(state, alias)
	}
}

func normalizeRelation(raw string) (string, bool) {
	relation, ok := relationAliases[normalizeForMatching(raw)]
	return relation, ok
}

func extractName(words []string) (string, bool) {
	nameParts := make([]string, 0, 2)
	for _, word := range words {
		if len(nameParts) == 2 {
			break
		}
		if _, stop := subjectLinkStopwords[word]; stop {
			break
		}
		if _, ok := normalizeRelation(word); ok {
			break
		}
		if !isNameToken(word) {
			break
		}
		nameParts = append(nameParts, word)
	}
	if len(nameParts) == 0 {
		return "", false
	}
	return strings.Join(nameParts, " "), true
}

func isNameToken(word string) bool {
	if word == "" {
		return false
	}
	for _, r := range word {
		switch {
		case r >= 'a' && r <= 'z':
		case r == '\'' || r == '-':
		default:
			return false
		}
	}
	return true
}

func subjectIDForName(name string) string {
	return "person:" + strings.ReplaceAll(normalizeForMatching(name), " ", "_")
}

func containsAlias(text, alias string) bool {
	haystack := " " + normalizeForMatching(text) + " "
	needle := " " + normalizeForMatching(alias) + " "
	return strings.Contains(haystack, needle)
}

func containsAnyAlias(text string, aliases []string) bool {
	for _, alias := range aliases {
		if containsAlias(text, alias) {
			return true
		}
	}
	return false
}

func looksLikeHealthSubjectText(text string) bool {
	padded := " " + normalizeForMatching(text) + " "
	for _, keyword := range healthSubjectKeywords {
		if strings.Contains(padded, keyword) {
			return true
		}
	}
	return false
}

func normalizeForMatching(text string) string {
	text = strings.NewReplacer("'s", "", "’s", "").Replace(text)
	replaced := strings.Map(func(r rune) rune {
		switch {
		case r >= 'A' && r <= 'Z':
			return r + ('a' - 'A')
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			return r
		default:
			return ' '
		}
	}, text)
	return strings.Join(strings.Fields(replaced), " ")
}

func isHealthKeyword(word string) bool {
	switch word {
	case "bmi", "bmr", "tdee", "weight", "height":
		return true
	default:
		return false
	}
}
