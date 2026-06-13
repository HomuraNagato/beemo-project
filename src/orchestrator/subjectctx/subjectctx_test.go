package subjectctx

import (
	"testing"

	pb "eve-beemo/proto/gen/proto"
)

func TestResolveLinksBrotherAndNameIntoSingleSubject(t *testing.T) {
	t.Parallel()

	ctx := Resolve([]*pb.ChatMessage{
		{Role: "user", Content: "I am Serene"},
		{Role: "assistant", Content: "Hi Serene."},
		{Role: "user", Content: "my brother Mark is 34 years old"},
		{Role: "assistant", Content: "noted"},
		{Role: "user", Content: "what is his bmi at 70kg and 180cm?"},
	})

	if got, want := ctx.CurrentSubjectID, "scoped:person_serene:brother:mark"; got != want {
		t.Fatalf("unexpected current subject: got %q want %q", got, want)
	}
	if len(ctx.Subjects) != 2 {
		t.Fatalf("unexpected subject count: %#v", ctx.Subjects)
	}
	aliases := map[string]struct{}{}
	for _, subject := range ctx.Subjects {
		if subject.ID != "scoped:person_serene:brother:mark" {
			continue
		}
		for _, alias := range subject.Aliases {
			aliases[alias] = struct{}{}
		}
	}
	for _, alias := range []string{"mark"} {
		if _, ok := aliases[alias]; !ok {
			t.Fatalf("missing alias %q in %#v", alias, ctx.Subjects[0].Aliases)
		}
	}
	for _, alias := range []string{"brother", "my brother"} {
		if _, ok := aliases[alias]; ok {
			t.Fatalf("relationship label %q should not be an identity alias: %#v", alias, ctx.Subjects[0].Aliases)
		}
	}
}

func TestResolveLeavesBrotherAliasAmbiguousAcrossTwoSubjects(t *testing.T) {
	t.Parallel()

	ctx := Resolve([]*pb.ChatMessage{
		{Role: "user", Content: "my brother Mark is 34"},
		{Role: "assistant", Content: "noted"},
		{Role: "user", Content: "my brother John is 29"},
		{Role: "assistant", Content: "noted"},
		{Role: "user", Content: "what is my brother's bmi?"},
	})

	if ctx.CurrentSubjectID != "" {
		t.Fatalf("expected ambiguous brother reference to stay unresolved, got %q", ctx.CurrentSubjectID)
	}
}

func TestResolveUsesIntroducedSpeakerForMyQueries(t *testing.T) {
	t.Parallel()

	ctx := Resolve([]*pb.ChatMessage{
		{Role: "user", Content: "I'm Serene"},
		{Role: "assistant", Content: "Hi Serene."},
		{Role: "user", Content: "what is my tdee?"},
	})

	if got, want := ctx.CurrentSubjectID, "person:serene"; got != want {
		t.Fatalf("unexpected current subject: got %q want %q", got, want)
	}
	if len(ctx.Subjects) != 1 || ctx.Subjects[0].ID != "person:serene" {
		t.Fatalf("expected introduced speaker subject, got %#v", ctx.Subjects)
	}
}

func TestResolveDoesNotTreatRememberAsNamedSubjectBeforeBMI(t *testing.T) {
	t.Parallel()

	ctx := Resolve([]*pb.ChatMessage{
		{Role: "user", Content: "my name is Serene"},
		{Role: "assistant", Content: "Hi Serene."},
		{Role: "user", Content: "do you remember my BMI?"},
	})

	if got, want := ctx.CurrentSubjectID, "person:serene"; got != want {
		t.Fatalf("unexpected current subject: got %q want %q", got, want)
	}
	for _, subject := range ctx.Subjects {
		if subject.ID == "person:remember" {
			t.Fatalf("unexpected bogus subject: %#v", ctx.Subjects)
		}
	}
}

func TestResolveIgnoresSeededCommandWordAliasForSelfFact(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		alias string
		text  string
	}{
		{alias: "remember", text: "please remember my codename is Moonrise"},
		{alias: "know", text: "do you know my project motto?"},
		{alias: "set", text: "set my project motto to steady sparks"},
	} {
		tc := tc
		t.Run(tc.alias, func(t *testing.T) {
			t.Parallel()

			ctx := ResolveWithIdentityContext([]*pb.ChatMessage{
				{Role: "user", Content: tc.text},
			}, []Subject{
				{ID: "person:serene", Aliases: []string{"serene"}},
				{ID: "person:" + tc.alias, Aliases: []string{tc.alias}},
			}, nil, "person:serene")

			if got, want := ctx.CurrentSubjectID, "person:serene"; got != want {
				t.Fatalf("unexpected current subject: got %q want %q", got, want)
			}
		})
	}
}

func TestResolveCreatesDirectNamedSubjectForHealthQuery(t *testing.T) {
	t.Parallel()

	ctx := Resolve([]*pb.ChatMessage{
		{Role: "user", Content: "what is the bmi of serene that has a height of 174cm and 134lbs?"},
		{Role: "assistant", Content: "The BMI is 20.08."},
		{Role: "user", Content: "what is her tdee?"},
	})

	if got, want := ctx.CurrentSubjectID, "person:serene"; got != want {
		t.Fatalf("unexpected current subject: got %q want %q", got, want)
	}
	if len(ctx.Subjects) != 1 {
		t.Fatalf("unexpected subject count: %#v", ctx.Subjects)
	}
	if len(ctx.Subjects[0].Aliases) != 1 || ctx.Subjects[0].Aliases[0] != "serene" {
		t.Fatalf("unexpected aliases: %#v", ctx.Subjects[0].Aliases)
	}
}

func TestResolveCreatesDirectNamedSubjectForPossessiveHealthQuery(t *testing.T) {
	t.Parallel()

	ctx := Resolve([]*pb.ChatMessage{
		{Role: "user", Content: "what is serene's bmi with 134lbs and 174cm?"},
	})

	if got, want := ctx.CurrentSubjectID, "person:serene"; got != want {
		t.Fatalf("unexpected current subject: got %q want %q", got, want)
	}
	if len(ctx.Subjects) != 1 {
		t.Fatalf("unexpected subject count: %#v", ctx.Subjects)
	}
	if len(ctx.Subjects[0].Aliases) != 1 || ctx.Subjects[0].Aliases[0] != "serene" {
		t.Fatalf("unexpected aliases: %#v", ctx.Subjects[0].Aliases)
	}
}

func TestResolveDoesNotTreatAboutAsNamedSubject(t *testing.T) {
	t.Parallel()

	ctx := Resolve([]*pb.ChatMessage{
		{Role: "user", Content: "what is the bmi of 45kg and 64 inches?"},
		{Role: "assistant", Content: "The BMI is 17.03."},
		{Role: "user", Content: "what about bmr for a 34 year old female?"},
	})

	if ctx.CurrentSubjectID != "" {
		t.Fatalf("expected no concrete subject, got %q", ctx.CurrentSubjectID)
	}
	for _, subject := range ctx.Subjects {
		if subject.ID == "person:about" {
			t.Fatalf("unexpected bogus subject: %#v", ctx.Subjects)
		}
	}
}

func TestResolveWithSeedUsesPersistedAliasesAcrossSessions(t *testing.T) {
	t.Parallel()

	ctx := ResolveWithSeed([]*pb.ChatMessage{
		{Role: "user", Content: "what is serene's bmr?"},
	}, []Subject{
		{ID: "person:serene", Aliases: []string{"serene", "sister", "my sister"}},
	})

	if got, want := ctx.CurrentSubjectID, "person:serene"; got != want {
		t.Fatalf("unexpected current subject: got %q want %q", got, want)
	}
	if len(ctx.Subjects) != 1 {
		t.Fatalf("unexpected subjects: %#v", ctx.Subjects)
	}
}

func TestResolveMyGirlfriendIsScopedToCurrentSpeaker(t *testing.T) {
	t.Parallel()

	ctx := Resolve([]*pb.ChatMessage{
		{Role: "user", Content: "I am Serene"},
		{Role: "assistant", Content: "Hi Serene."},
		{Role: "user", Content: "my girlfriend Sabrina is 46kg and 162cm"},
		{Role: "assistant", Content: "noted"},
		{Role: "user", Content: "what is my girlfriend's BMI?"},
	})

	if got, want := ctx.CurrentSubjectID, "scoped:person_serene:girlfriend:sabrina"; got != want {
		t.Fatalf("unexpected current subject: got %q want %q", got, want)
	}
}

func TestResolveNamedBranchMentionUsesActiveSpeakerTree(t *testing.T) {
	t.Parallel()

	ctx := Resolve([]*pb.ChatMessage{
		{Role: "user", Content: "I am Serene"},
		{Role: "assistant", Content: "Hi Serene."},
		{Role: "user", Content: "my girlfriend is Sabrina"},
		{Role: "assistant", Content: "noted"},
		{Role: "user", Content: "Sabrina weighs 46kg and is 162cm tall"},
		{Role: "assistant", Content: "noted"},
		{Role: "user", Content: "what is Sabrina's BMI?"},
	})

	if got, want := ctx.CurrentSubjectID, "scoped:person_serene:girlfriend:sabrina"; got != want {
		t.Fatalf("unexpected current subject: got %q want %q", got, want)
	}
}

func TestResolveNamedBranchMentionWinsOverStandaloneIdentityForActiveSpeaker(t *testing.T) {
	t.Parallel()

	ctx := Resolve([]*pb.ChatMessage{
		{Role: "user", Content: "I am Serene"},
		{Role: "assistant", Content: "Hi Serene."},
		{Role: "user", Content: "my girlfriend is Sabrina"},
		{Role: "assistant", Content: "noted"},
		{Role: "user", Content: "I am Sabrina"},
		{Role: "assistant", Content: "Hi Sabrina."},
		{Role: "user", Content: "Hey BeeMo, it's Serene again"},
		{Role: "assistant", Content: "Hi Serene."},
		{Role: "user", Content: "what is Sabrina's BMI?"},
	})

	if got, want := ctx.CurrentSubjectID, "scoped:person_serene:girlfriend:sabrina"; got != want {
		t.Fatalf("unexpected current subject: got %q want %q", got, want)
	}
}

func TestResolveSpeakerSwitchDoesNotInheritOtherSpeakerGirlfriend(t *testing.T) {
	t.Parallel()

	ctx := Resolve([]*pb.ChatMessage{
		{Role: "user", Content: "I am Serene"},
		{Role: "assistant", Content: "Hi Serene."},
		{Role: "user", Content: "my girlfriend is Sabrina"},
		{Role: "assistant", Content: "noted"},
		{Role: "user", Content: "I am Sabrina"},
		{Role: "assistant", Content: "Hi Sabrina."},
		{Role: "user", Content: "what is my girlfriend's BMI?"},
	})

	if got := ctx.CurrentSubjectID; got != "" {
		t.Fatalf("Sabrina should not inherit Serene's girlfriend relationship, got %q", got)
	}
}

func TestResolveUsesSeededPermanentRelationshipWithActiveSpeaker(t *testing.T) {
	t.Parallel()

	ctx := ResolveWithIdentityContext([]*pb.ChatMessage{
		{Role: "user", Content: "what is my girlfriend's BMI?"},
	}, []Subject{
		{ID: "person:serene", Aliases: []string{"serene"}},
		{ID: "person:sabrina", Aliases: []string{"sabrina"}},
	}, []Relationship{
		{OwnerID: "person:sabrina", Relation: "girlfriend", SubjectID: "scoped:person_sabrina:girlfriend:serene"},
	}, "person:sabrina")

	if got, want := ctx.CurrentSubjectID, "scoped:person_sabrina:girlfriend:serene"; got != want {
		t.Fatalf("unexpected current subject: got %q want %q", got, want)
	}
}

func TestResolveRelationshipLabelsAreScopedToLastIntroducedSpeakerTree(t *testing.T) {
	t.Parallel()

	ctx := Resolve([]*pb.ChatMessage{
		{Role: "user", Content: "I am Serene"},
		{Role: "assistant", Content: "Hi Serene."},
		{Role: "user", Content: "my mom is Nicole"},
		{Role: "assistant", Content: "noted"},
		{Role: "user", Content: "I am Sabrina"},
		{Role: "assistant", Content: "Hi Sabrina."},
		{Role: "user", Content: "my mom is Maureen"},
		{Role: "assistant", Content: "noted"},
		{Role: "user", Content: "what is my mom's BMI?"},
	})

	if got, want := ctx.CurrentSubjectID, "scoped:person_sabrina:mother:maureen"; got != want {
		t.Fatalf("unexpected current subject for Sabrina's mom: got %q want %q", got, want)
	}
	for _, subject := range ctx.Subjects {
		for _, alias := range subject.Aliases {
			if alias == "mom" || alias == "my mom" || alias == "mother" || alias == "my mother" {
				t.Fatalf("relationship label leaked into identity aliases: %#v", ctx.Subjects)
			}
		}
	}
}

func TestResolveItIsAgainSwitchesBackToSavedIdentityTree(t *testing.T) {
	t.Parallel()

	ctx := Resolve([]*pb.ChatMessage{
		{Role: "user", Content: "I am Serene"},
		{Role: "assistant", Content: "Hi Serene."},
		{Role: "user", Content: "my mom is Nicole"},
		{Role: "assistant", Content: "noted"},
		{Role: "user", Content: "I am Sabrina"},
		{Role: "assistant", Content: "Hi Sabrina."},
		{Role: "user", Content: "my mom is Maureen"},
		{Role: "assistant", Content: "noted"},
		{Role: "user", Content: "Hey BeeMo, it's Serene again"},
		{Role: "assistant", Content: "Hi Serene."},
		{Role: "user", Content: "what is my mom's BMI?"},
	})

	if got, want := ctx.CurrentSubjectID, "scoped:person_serene:mother:nicole"; got != want {
		t.Fatalf("unexpected current subject after switching back to Serene: got %q want %q", got, want)
	}
}

func TestResolveStandaloneRelationshipLabelUsesActiveSpeakerTree(t *testing.T) {
	t.Parallel()

	ctx := ResolveWithIdentityContext([]*pb.ChatMessage{
		{Role: "user", Content: "what is mom's BMI?"},
	}, []Subject{
		{ID: "scoped:person_serene:mother:nicole", Aliases: []string{"nicole"}},
		{ID: "scoped:person_sabrina:mother:maureen", Aliases: []string{"maureen"}},
		{ID: "person:serene", Aliases: []string{"serene"}},
		{ID: "person:sabrina", Aliases: []string{"sabrina"}},
	}, []Relationship{
		{OwnerID: "person:serene", Relation: "mother", SubjectID: "scoped:person_serene:mother:nicole"},
		{OwnerID: "person:sabrina", Relation: "mother", SubjectID: "scoped:person_sabrina:mother:maureen"},
	}, "person:serene")

	if got, want := ctx.CurrentSubjectID, "scoped:person_serene:mother:nicole"; got != want {
		t.Fatalf("unexpected current subject for Serene's standalone mom label: got %q want %q", got, want)
	}
}

func TestResolveNumberedFriendLabelsAreScopedSubIdentities(t *testing.T) {
	t.Parallel()

	ctx := Resolve([]*pb.ChatMessage{
		{Role: "user", Content: "I am Serene"},
		{Role: "assistant", Content: "Hi Serene."},
		{Role: "user", Content: "my friend1 is Ilona"},
		{Role: "assistant", Content: "noted"},
		{Role: "user", Content: "my friend2 is Alex"},
		{Role: "assistant", Content: "noted"},
		{Role: "user", Content: "what is my friend2 BMI?"},
	})

	if got, want := ctx.CurrentSubjectID, "scoped:person_serene:friend2:alex"; got != want {
		t.Fatalf("unexpected current subject for numbered friend label: got %q want %q", got, want)
	}
}

func TestResolveKeepsRepeatedRelationshipNameScopedToOriginalSpeaker(t *testing.T) {
	t.Parallel()

	ctx := Resolve([]*pb.ChatMessage{
		{Role: "user", Content: "I am Serene"},
		{Role: "assistant", Content: "Hi Serene."},
		{Role: "user", Content: "my girlfriend is Sabrina. Sabrina weighs 46kg and is 162cm tall"},
		{Role: "assistant", Content: "noted"},
		{Role: "user", Content: "I am Sabrina"},
		{Role: "assistant", Content: "Hi Sabrina."},
		{Role: "user", Content: "what is my girlfriend's BMI?"},
	})

	if got := ctx.CurrentSubjectID; got != "" {
		t.Fatalf("Sabrina should not inherit Serene's scoped girlfriend facts, got %q", got)
	}
	for _, subject := range ctx.Subjects {
		if subject.ID == "person:sabrina_sabrina" {
			t.Fatalf("unexpected repeated-name subject: %#v", ctx.Subjects)
		}
	}
}

func TestResolveFriendAndGirlfriendRemainDistinct(t *testing.T) {
	t.Parallel()

	ctx := Resolve([]*pb.ChatMessage{
		{Role: "user", Content: "I am Serene"},
		{Role: "assistant", Content: "Hi Serene."},
		{Role: "user", Content: "my girlfriend Sabrina is 46kg and 162cm"},
		{Role: "assistant", Content: "noted"},
		{Role: "user", Content: "my friend Ilona is 130lbs and 5'8\""},
		{Role: "assistant", Content: "noted"},
		{Role: "user", Content: "what is my friend Ilona's BMI?"},
	})

	if got, want := ctx.CurrentSubjectID, "scoped:person_serene:friend:ilona"; got != want {
		t.Fatalf("unexpected current subject: got %q want %q", got, want)
	}
	for _, subject := range ctx.Subjects {
		if subject.ID == "scoped:person_serene:girlfriend:sabrina" {
			if !containsString(subject.Aliases, "sabrina") {
				t.Fatalf("missing Sabrina alias: %#v", subject.Aliases)
			}
		}
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
