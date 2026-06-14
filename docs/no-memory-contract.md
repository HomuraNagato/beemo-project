# No-Memory Contract

This contract defines what Beemo should do before long-term memory is added back.
The goal is to make the current session reliable without storing permanent facts.

## Scope

Beemo may use:

- The latest user message.
- The active in-session transcript.
- Pending tool clarification state.
- Tool results from the current turn.
- Optional speaker labels stated in the current session.

Beemo must not use:

- Durable identity files.
- Cross-session facts.
- Permanent relationship graphs.
- Facts from previous app runs unless they are explicitly present in the current transcript.

## Expected Behavior

Beemo should answer using current-session context when the needed facts are present.

Example:

```text
User: My girlfriend is Sabrina.
User: What is my girlfriend's name?
Beemo: Your girlfriend's name is Sabrina.
```

Beemo should ask when the current session does not contain enough context.

Example:

```text
User: What is my girlfriend's BMI?
Beemo: I need your girlfriend's height and weight to calculate that.
```

Beemo should keep pending clarifications tied to the current session.

Example:

```text
User: What is my BMI if I weigh 130 lb?
Beemo: What is the height?
User: 5'8"
Beemo: Your BMI is 19.77.
```

## Non-Goals

These should not work in pass 1:

- Remembering personal facts after restart.
- Automatically merging people across sessions.
- Resolving "mom", "girlfriend", or "my" from durable identity storage.
- Saving new permanent facts.
- Building relationship trees.

## Design Rule

Long-term memory should later plug into context assembly as one input. It should not be mixed directly into routing, tool execution, or response rendering.
