package subjectctx

import (
	"testing"

	pb "eve-beemo/proto/gen/proto"
)

func TestResolveLinksBrotherAndNameIntoSingleSubject(t *testing.T) {
	t.Parallel()

	ctx := Resolve([]*pb.ChatMessage{
		{Role: "user", Content: "my brother Mark is 34 years old"},
		{Role: "assistant", Content: "noted"},
		{Role: "user", Content: "what is his bmi at 70kg and 180cm?"},
	})

	if got, want := ctx.CurrentSubjectID, "person:mark"; got != want {
		t.Fatalf("unexpected current subject: got %q want %q", got, want)
	}
	if len(ctx.Subjects) != 1 {
		t.Fatalf("unexpected subject count: %#v", ctx.Subjects)
	}
	aliases := map[string]struct{}{}
	for _, alias := range ctx.Subjects[0].Aliases {
		aliases[alias] = struct{}{}
	}
	for _, alias := range []string{"mark", "brother", "my brother"} {
		if _, ok := aliases[alias]; !ok {
			t.Fatalf("missing alias %q in %#v", alias, ctx.Subjects[0].Aliases)
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

func TestResolveUsesSelfSubjectForMyQueries(t *testing.T) {
	t.Parallel()

	ctx := Resolve([]*pb.ChatMessage{
		{Role: "user", Content: "what is my tdee?"},
	})

	if got, want := ctx.CurrentSubjectID, selfSubjectID; got != want {
		t.Fatalf("unexpected current subject: got %q want %q", got, want)
	}
	if len(ctx.Subjects) != 1 || ctx.Subjects[0].ID != selfSubjectID {
		t.Fatalf("expected self subject, got %#v", ctx.Subjects)
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
