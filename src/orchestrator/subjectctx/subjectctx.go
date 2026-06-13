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
		"a": {}, "about": {}, "an": {}, "and": {}, "at": {}, "do": {}, "for": {}, "from": {}, "has": {}, "have": {}, "he": {}, "her": {},
		"his": {}, "i": {}, "if": {}, "in": {}, "is": {}, "me": {}, "mine": {}, "my": {}, "of": {}, "she": {}, "that": {}, "the": {},
		"their": {}, "they": {}, "was": {}, "weighs": {}, "weighed": {}, "weighing": {},
		"again": {},
		"bmi":   {}, "bmr": {}, "tdee": {}, "weight": {}, "height": {},
		"change": {}, "correct": {}, "female": {}, "know": {}, "male": {}, "old": {}, "please": {}, "recall": {}, "remember": {},
		"remembered": {}, "remind": {}, "set": {}, "update": {}, "what": {}, "who": {}, "with": {}, "year": {}, "years": {}, "you": {},
	}
	relationAliases = map[string]string{
		"brother":    "brother",
		"dad":        "father",
		"daughter":   "daughter",
		"father":     "father",
		"friend":     "friend",
		"girlfriend": "girlfriend",
		"boyfriend":  "boyfriend",
		"husband":    "husband",
		"mom":        "mother",
		"mother":     "mother",
		"partner":    "partner",
		"sister":     "sister",
		"son":        "son",
		"trainer":    "trainer",
		"wife":       "wife",
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

type Relationship struct {
	OwnerID   string
	Relation  string
	SubjectID string
}

type Context struct {
	CurrentSubjectID string
	Subjects         []Subject
	Relationships    []Relationship
}

func (c Context) Summary() string {
	if c.CurrentSubjectID == "" && len(c.Subjects) == 0 {
		return ""
	}

	lines := make([]string, 0, len(c.Subjects)+1)
	if c.CurrentSubjectID != "" {
		lines = append(lines, fmt.Sprintf("current_subject_id: %s", c.CurrentSubjectID))
	}
	focusedSubjectIDs := c.focusedSubjectIDs()
	for _, subject := range c.Subjects {
		if len(focusedSubjectIDs) > 0 {
			if _, ok := focusedSubjectIDs[subject.ID]; !ok {
				continue
			}
		}
		lines = append(lines, fmt.Sprintf("- subject_id: %s aliases: %s", subject.ID, strings.Join(subject.Aliases, ", ")))
	}
	for _, relationship := range c.Relationships {
		if len(focusedSubjectIDs) > 0 {
			if _, ownerOK := focusedSubjectIDs[relationship.OwnerID]; !ownerOK {
				if _, targetOK := focusedSubjectIDs[relationship.SubjectID]; !targetOK {
					continue
				}
			}
		}
		lines = append(lines, fmt.Sprintf("- relationship: %s %s %s", relationship.OwnerID, relationship.Relation, relationship.SubjectID))
	}
	return strings.Join(lines, "\n")
}

func (c Context) focusedSubjectIDs() map[string]struct{} {
	currentSubjectID := strings.TrimSpace(c.CurrentSubjectID)
	if currentSubjectID == "" {
		return nil
	}
	ids := map[string]struct{}{currentSubjectID: {}}
	for _, relationship := range c.Relationships {
		if relationship.OwnerID == currentSubjectID {
			ids[relationship.SubjectID] = struct{}{}
		}
		if relationship.SubjectID == currentSubjectID {
			ids[relationship.OwnerID] = struct{}{}
		}
	}
	return ids
}

func Resolve(messages []*pb.ChatMessage) Context {
	return ResolveWithSeed(messages, nil)
}

func ResolveWithSeed(messages []*pb.ChatMessage, seeded []Subject) Context {
	return ResolveWithIdentityContext(messages, seeded, nil, "")
}

func ResolveWithIdentityContext(messages []*pb.ChatMessage, seeded []Subject, relationships []Relationship, activeSpeakerSubjectID string) Context {
	r := resolver{
		subjects:               map[string]*subjectState{},
		aliasToIDs:             map[string]map[string]struct{}{},
		activeSpeakerSubjectID: strings.TrimSpace(activeSpeakerSubjectID),
	}
	for _, subject := range seeded {
		r.seedSubject(subject)
	}
	for _, relationship := range relationships {
		r.seedRelationship(relationship)
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
		if subjectID := r.linkSpeakerIntroduction(text); subjectID != "" {
			r.activeSpeakerSubjectID = subjectID
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
		Relationships:    r.relationships(),
	}
}

type resolver struct {
	subjects               map[string]*subjectState
	aliasToIDs             map[string]map[string]struct{}
	relationsByOwner       map[string]map[string][]string
	order                  []string
	lastNonSelfSubjectID   string
	activeSpeakerSubjectID string
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

func (r *resolver) seedRelationship(relationship Relationship) {
	ownerID := strings.TrimSpace(relationship.OwnerID)
	relation := strings.TrimSpace(relationship.Relation)
	subjectID := strings.TrimSpace(relationship.SubjectID)
	if ownerID == "" || relation == "" || subjectID == "" || ownerID == selfSubjectID || subjectID == selfSubjectID {
		return
	}
	r.ensureSubjectID(ownerID)
	r.ensureSubjectID(subjectID)
	r.addRelation(ownerID, relation, subjectID)
}

func (r *resolver) ensureSubjectID(subjectID string) *subjectState {
	subjectID = strings.TrimSpace(subjectID)
	if subjectID == "" {
		return nil
	}
	state, exists := r.subjects[subjectID]
	if !exists {
		state = &subjectState{ID: subjectID}
		r.subjects[subjectID] = state
		r.order = append(r.order, subjectID)
	}
	return state
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
			if idx+3 < len(words) && words[idx+2] == "is" {
				if name, ok := extractName(words[idx+3:]); ok {
					r.registerLinkedSubject(name, relation, r.activeSpeakerSubjectID)
				}
				continue
			}
			if name, ok := extractName(words[idx+2:]); ok {
				r.registerLinkedSubject(name, relation, r.activeSpeakerSubjectID)
			}
			continue
		}
		if idx+3 < len(words) && words[idx+1] == "is" && words[idx+2] == "my" {
			relation, ok := normalizeRelation(words[idx+3])
			if !ok {
				continue
			}
			if name, ok := extractName(words[idx:]); ok {
				r.registerLinkedSubject(name, relation, r.activeSpeakerSubjectID)
			}
		}
		if idx+3 < len(words) && words[idx+2] == "is" {
			relation, ok := normalizeRelation(words[idx+1])
			if !ok {
				continue
			}
			owner, ok := extractName(words[idx : idx+1])
			if !ok {
				continue
			}
			if name, ok := extractName(words[idx+3:]); ok {
				ownerID := r.registerNamedSubject(owner)
				r.registerLinkedSubject(name, relation, ownerID)
			}
		}
	}
}

func (r *resolver) registerLinkedSubject(name, relation, ownerID string) {
	subjectID := subjectIDForName(name)
	if strings.TrimSpace(ownerID) != "" {
		subjectID = scopedRelationshipSubjectID(ownerID, relation, name)
	}
	state := r.ensureSubjectID(subjectID)
	r.addAlias(state, name)
	if strings.TrimSpace(ownerID) != "" {
		r.addRelation(ownerID, relation, subjectID)
	}
}

func (r *resolver) addRelation(ownerID, relation, subjectID string) {
	ownerID = strings.TrimSpace(ownerID)
	relation = strings.TrimSpace(relation)
	subjectID = strings.TrimSpace(subjectID)
	if ownerID == "" || relation == "" || subjectID == "" {
		return
	}
	if r.relationsByOwner == nil {
		r.relationsByOwner = map[string]map[string][]string{}
	}
	if r.relationsByOwner[ownerID] == nil {
		r.relationsByOwner[ownerID] = map[string][]string{}
	}
	for _, existing := range r.relationsByOwner[ownerID][relation] {
		if existing == subjectID {
			return
		}
	}
	r.relationsByOwner[ownerID][relation] = append(r.relationsByOwner[ownerID][relation], subjectID)
}

func (r *resolver) relationships() []Relationship {
	if len(r.relationsByOwner) == 0 {
		return nil
	}
	ownerIDs := make([]string, 0, len(r.relationsByOwner))
	for ownerID := range r.relationsByOwner {
		ownerIDs = append(ownerIDs, ownerID)
	}
	sort.Strings(ownerIDs)
	relationships := []Relationship{}
	for _, ownerID := range ownerIDs {
		relations := r.relationsByOwner[ownerID]
		names := make([]string, 0, len(relations))
		for relation := range relations {
			names = append(names, relation)
		}
		sort.Strings(names)
		for _, relation := range names {
			targets := append([]string(nil), relations[relation]...)
			sort.Strings(targets)
			for _, targetID := range targets {
				relationships = append(relationships, Relationship{
					OwnerID:   ownerID,
					Relation:  relation,
					SubjectID: targetID,
				})
			}
		}
	}
	return relationships
}

func (r *resolver) registerNamedSubject(name string) string {
	subjectID := subjectIDForName(name)
	state, exists := r.subjects[subjectID]
	if !exists {
		state = &subjectState{ID: subjectID}
		r.subjects[subjectID] = state
		r.order = append(r.order, subjectID)
	}
	r.addAlias(state, name)
	return subjectID
}

func (r *resolver) registerMentionedSubject(name string) string {
	if subjectID, ok := r.activeTreeTargetForAlias(name); ok {
		state := r.ensureSubjectID(subjectID)
		r.addAlias(state, name)
		return subjectID
	}
	return r.registerNamedSubject(name)
}

func (r *resolver) linkSpeakerIntroduction(text string) string {
	words := strings.Fields(text)
	for idx := 0; idx < len(words); idx++ {
		switch {
		case idx+2 < len(words) && words[idx] == "i" && (words[idx+1] == "am" || words[idx+1] == "m"):
			if name, ok := extractName(words[idx+2:]); ok {
				r.registerNamedSubject(name)
				return subjectIDForName(name)
			}
		case idx+3 < len(words) && words[idx] == "my" && words[idx+1] == "name" && words[idx+2] == "is":
			if name, ok := extractName(words[idx+3:]); ok {
				r.registerNamedSubject(name)
				return subjectIDForName(name)
			}
		case idx+2 < len(words) && words[idx] == "this" && words[idx+1] == "is":
			if name, ok := extractName(words[idx+2:]); ok {
				r.registerNamedSubject(name)
				return subjectIDForName(name)
			}
		case idx+2 < len(words) && words[idx] == "it" && (words[idx+1] == "is" || words[idx+1] == "s"):
			if name, ok := extractName(words[idx+2:]); ok {
				r.registerNamedSubject(name)
				return subjectIDForName(name)
			}
		case idx+1 < len(words) && (words[idx] == "it" || words[idx] == "its"):
			if name, ok := extractName(words[idx+1:]); ok {
				r.registerNamedSubject(name)
				return subjectIDForName(name)
			}
		}
	}
	return ""
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
		if !canResolveSubjectMentionAlias(alias) {
			continue
		}
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
			r.registerMentionedSubject(name)
		}
	}
	for idx, word := range words {
		if !isHealthKeyword(word) || idx == 0 {
			continue
		}
		for start := max(0, idx-2); start < idx; start++ {
			if name, ok := extractName(words[start:idx]); ok {
				r.registerMentionedSubject(name)
				break
			}
		}
	}
}

func (r *resolver) inferCurrentSubject(text string) string {
	if text == "" {
		return ""
	}
	if subjectID := r.inferSpeakerRelation(text); subjectID != "" {
		return subjectID
	}
	if subjectID := r.inferActiveTreeMention(text); subjectID != "" {
		return subjectID
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
	if textMentionsRelationLabel(text) {
		return ""
	}
	if containsAnyAlias(text, selfPronouns) {
		return r.activeSpeakerSubjectID
	}
	return ""
}

func (r *resolver) inferActiveTreeMention(text string) string {
	mentioned := r.activeTreeMentionedIDs(text)
	if len(mentioned) != 1 {
		return ""
	}
	return mentioned[0]
}

func (r *resolver) activeTreeMentionedIDs(text string) []string {
	ownerID := strings.TrimSpace(r.activeSpeakerSubjectID)
	if ownerID == "" || len(r.relationsByOwner[ownerID]) == 0 {
		return nil
	}
	matches := map[string]struct{}{}
	for _, targets := range r.relationsByOwner[ownerID] {
		for _, targetID := range targets {
			if r.subjectIDMatchesText(targetID, text) {
				matches[targetID] = struct{}{}
			}
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

func (r *resolver) activeTreeTargetForAlias(alias string) (string, bool) {
	ownerID := strings.TrimSpace(r.activeSpeakerSubjectID)
	normalized := normalizeForMatching(alias)
	if ownerID == "" || normalized == "" || len(r.relationsByOwner[ownerID]) == 0 {
		return "", false
	}
	matches := map[string]struct{}{}
	for _, targets := range r.relationsByOwner[ownerID] {
		for _, targetID := range targets {
			if r.subjectHasAlias(targetID, normalized) {
				matches[targetID] = struct{}{}
			}
		}
	}
	if len(matches) != 1 {
		return "", false
	}
	for subjectID := range matches {
		return subjectID, true
	}
	return "", false
}

func (r *resolver) subjectIDMatchesText(subjectID, text string) bool {
	state := r.subjects[subjectID]
	if state != nil {
		for _, alias := range state.Aliases {
			if containsAlias(text, alias) {
				return true
			}
		}
	}
	if alias := fallbackAliasForSubjectID(subjectID); alias != "" {
		return containsAlias(text, alias)
	}
	return false
}

func (r *resolver) subjectHasAlias(subjectID, alias string) bool {
	state := r.subjects[subjectID]
	if state != nil {
		for _, existing := range state.Aliases {
			if normalizeForMatching(existing) == alias {
				return true
			}
		}
	}
	return fallbackAliasForSubjectID(subjectID) == alias
}

func (r *resolver) inferSpeakerRelation(text string) string {
	ownerID := strings.TrimSpace(r.activeSpeakerSubjectID)
	if ownerID == "" {
		return ""
	}
	relations := r.relationsByOwner[ownerID]
	if len(relations) == 0 {
		return ""
	}
	words := strings.Fields(text)
	for idx := 0; idx < len(words); idx++ {
		relationWord := words[idx]
		if words[idx] == "my" || words[idx] == "mine" {
			if idx+1 >= len(words) {
				continue
			}
			relationWord = words[idx+1]
		}
		relation, ok := normalizeRelation(relationWord)
		if !ok {
			continue
		}
		targets := relations[relation]
		if len(targets) == 1 {
			return strings.TrimSpace(targets[0])
		}
	}
	return ""
}

func (r *resolver) hasAmbiguousAliasMention(text string) bool {
	for alias, subjectIDs := range r.aliasToIDs {
		if !canResolveSubjectMentionAlias(alias) {
			continue
		}
		if len(subjectIDs) <= 1 {
			continue
		}
		if containsAlias(text, alias) {
			return true
		}
	}
	return false
}

func canResolveSubjectMentionAlias(alias string) bool {
	alias = normalizeForMatching(alias)
	if alias == "" {
		return false
	}
	if _, stop := subjectLinkStopwords[alias]; stop {
		return false
	}
	return true
}

func textMentionsRelationLabel(text string) bool {
	words := strings.Fields(text)
	for idx := 0; idx < len(words); idx++ {
		relationWord := words[idx]
		if words[idx] == "my" || words[idx] == "mine" {
			if idx+1 >= len(words) {
				continue
			}
			relationWord = words[idx+1]
		}
		if _, ok := normalizeRelation(relationWord); ok {
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
	normalized := normalizeForMatching(raw)
	if strings.HasPrefix(normalized, "friend") && len(normalized) > len("friend") {
		suffix := strings.TrimPrefix(normalized, "friend")
		for _, r := range suffix {
			if r < '0' || r > '9' {
				return "", false
			}
		}
		return normalized, true
	}
	relation, ok := relationAliases[normalized]
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
		if len(nameParts) > 0 && nameParts[len(nameParts)-1] == word {
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
	for idx, r := range word {
		switch {
		case r >= 'a' && r <= 'z':
		case idx > 0 && r >= '0' && r <= '9':
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

func fallbackAliasForSubjectID(subjectID string) string {
	subjectID = strings.TrimSpace(subjectID)
	if subjectID == "" {
		return ""
	}
	parts := strings.Split(subjectID, ":")
	if len(parts) == 0 {
		return ""
	}
	last := strings.TrimSpace(parts[len(parts)-1])
	if last == "" {
		return ""
	}
	return normalizeForMatching(strings.ReplaceAll(last, "_", " "))
}

func scopedRelationshipSubjectID(ownerID, relation, name string) string {
	owner := strings.NewReplacer(":", "_", " ", "_").Replace(normalizeForMatching(ownerID))
	relation = strings.ReplaceAll(normalizeForMatching(relation), " ", "_")
	name = strings.ReplaceAll(normalizeForMatching(name), " ", "_")
	if owner == "" || relation == "" || name == "" {
		return subjectIDForName(name)
	}
	return "scoped:" + owner + ":" + relation + ":" + name
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
