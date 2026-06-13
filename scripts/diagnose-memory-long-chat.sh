#!/usr/bin/env bash
set -u

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ORCH_CONTAINER="${ORCH_CONTAINER:-eve-orchestrator}"
DB_CONTAINER="${DB_CONTAINER:-pensieve}"
SESSION_PREFIX="${SESSION_PREFIX:-diag-long}"
RUN_ID="${RUN_ID:-$(date +%Y%m%d%H%M%S)}"

pass_count=0
fail_count=0

chat() {
  local session="$1"
  local text="$2"
  local payload
  payload="$(jq -cn --arg session "$session" --arg text "$text" '{session_id:$session,messages:[{role:"user",content:$text}]}')"
  docker exec -i "$ORCH_CONTAINER" grpcurl -plaintext -proto proto/agent.proto -d "$payload" localhost:5013 eve.Orchestrator/Chat 2>&1
}

chat_text() {
  local session="$1"
  local text="$2"
  local raw
  raw="$(chat "$session" "$text")"
  if jq -e . >/dev/null 2>&1 <<<"$raw"; then
    jq -r '.text // ""' <<<"$raw"
  else
    printf 'ERROR: %s' "$raw"
  fi
}

expect_contains() {
  local label="$1"
  local got="$2"
  local want="$3"
  if [[ "$got" == *"$want"* ]]; then
    printf 'PASS %-42s -> %s\n' "$label" "$got"
    pass_count=$((pass_count + 1))
  else
    printf 'FAIL %-42s -> got=%q want_contains=%q\n' "$label" "$got" "$want"
    fail_count=$((fail_count + 1))
  fi
}

expect_not_contains() {
  local label="$1"
  local got="$2"
  local bad="$3"
  if [[ "$got" != *"$bad"* ]]; then
    printf 'PASS %-42s -> %s\n' "$label" "$got"
    pass_count=$((pass_count + 1))
  else
    printf 'FAIL %-42s -> got=%q bad_contains=%q\n' "$label" "$got" "$bad"
    fail_count=$((fail_count + 1))
  fi
}

sql() {
  docker exec -i "$DB_CONTAINER" psql -U postgres -d beemo -Atc "$1"
}

printf 'Beemo long-memory diagnostic run_id=%s\n' "$RUN_ID"
printf 'Using containers: orchestrator=%s db=%s\n\n' "$ORCH_CONTAINER" "$DB_CONTAINER"

session_a="${SESSION_PREFIX}-facts-${RUN_ID}"
subject_a="person:diaglongserene${RUN_ID}"
speaker_a="DiagLongSerene${RUN_ID}"

printf 'Scenario A: 80 direct facts, late recall, and correction\n'
chat_text "$session_a" "I am ${speaker_a}" >/dev/null
for idx in $(seq -f "%03g" 1 80); do
  chat_text "$session_a" "my detail ${idx} is value-${idx}" >/dev/null
done
response="$(chat_text "$session_a" "what is my detail 042?")"
expect_contains "A recall detail 042" "$response" "value-042"
chat_text "$session_a" "that's wrong, my detail 042 is corrected-042" >/dev/null
response="$(chat_text "$session_a" "what is my detail 042?")"
expect_contains "A recall corrected detail 042" "$response" "corrected-042"
response="$(chat_text "$session_a" "what did I say my detail 042 was?")"
expect_contains "A freeform recall corrected detail 042" "$response" "corrected-042"

stored_count="$(sql "SELECT COUNT(*) FROM observations WHERE session_id='${session_a}' AND subject_id='${subject_a}';")"
printf 'INFO Scenario A stored_observations=%s session=%s subject=%s\n\n' "$stored_count" "$session_a" "$subject_a"

session_b="${SESSION_PREFIX}-identity-${RUN_ID}"
serene_b="DiagSwitchSerene${RUN_ID}"
sabrina_b="DiagSwitchSabrina${RUN_ID}"
subject_serene_b="person:diagswitchserene${RUN_ID}"
subject_sabrina_b="person:diagswitchsabrina${RUN_ID}"

printf 'Scenario B: identity switching and isolated facts\n'
chat_text "$session_b" "I am ${serene_b}. my favorite food is mango rice and my lucky number is 7" >/dev/null
response="$(chat_text "$session_b" "what is my favorite food?")"
expect_contains "B Serene favorite food" "$response" "mango rice"
chat_text "$session_b" "Hi BMO, I am ${sabrina_b}. my favorite food is lychee cake and my lucky number is 9" >/dev/null
response="$(chat_text "$session_b" "what is my favorite food?")"
expect_contains "B Sabrina favorite food" "$response" "lychee cake"
expect_not_contains "B Sabrina not Serene food" "$response" "mango rice"
chat_text "$session_b" "Hey BMO, it is ${serene_b} again" >/dev/null
response="$(chat_text "$session_b" "what is my lucky number?")"
expect_contains "B Serene lucky number after switch" "$response" "7"
expect_not_contains "B Serene not Sabrina number" "$response" "9"
serene_food="$(sql "SELECT raw_value::text FROM observations WHERE subject_id='${subject_serene_b}' AND attribute='favorite_food' ORDER BY created_at DESC, id DESC LIMIT 1;")"
sabrina_food="$(sql "SELECT raw_value::text FROM observations WHERE subject_id='${subject_sabrina_b}' AND attribute='favorite_food' ORDER BY created_at DESC, id DESC LIMIT 1;")"
printf 'INFO Scenario B db_serene_favorite_food=%s db_sabrina_favorite_food=%s\n\n' "$serene_food" "$sabrina_food"

session_c="${SESSION_PREFIX}-relation-${RUN_ID}"
serene_c="DiagRelSerene${RUN_ID}"
sabrina_c="DiagRelSabrina${RUN_ID}"
scoped_c="scoped:person_diagrelserene${RUN_ID}:girlfriend:diagrelsabrina${RUN_ID}"

printf 'Scenario C: scoped relationship facts through long distractor sequence\n'
chat_text "$session_c" "I am ${serene_c}. My girlfriend is ${sabrina_c}" >/dev/null
chat_text "$session_c" "${sabrina_c} weighs 46kg and is 162cm tall" >/dev/null
for idx in $(seq -w 1 30); do
  chat_text "$session_c" "my filler memory ${idx} is filler-${idx}" >/dev/null
done
response="$(chat_text "$session_c" "what is my girlfriend's BMI?")"
expect_contains "C girlfriend BMI after distractors" "$response" "17.53"
rel_weight="$(sql "SELECT raw_value::text FROM observations WHERE subject_id='${scoped_c}' AND attribute='weight' ORDER BY created_at DESC, id DESC LIMIT 1;")"
rel_height="$(sql "SELECT raw_value::text FROM observations WHERE subject_id='${scoped_c}' AND attribute='height' ORDER BY created_at DESC, id DESC LIMIT 1;")"
printf 'INFO Scenario C scoped_weight=%s scoped_height=%s scoped_subject=%s\n\n' "$rel_weight" "$rel_height" "$scoped_c"

session_d="${SESSION_PREFIX}-phrases-${RUN_ID}"
speaker_d="DiagPhraseSerene${RUN_ID}"
subject_d="person:diagphraseserene${RUN_ID}"

printf 'Scenario D: generic memory phrase variants\n'
chat_text "$session_d" "I am ${speaker_d}" >/dev/null
chat_text "$session_d" "please remember my codename is Moonrise" >/dev/null
response="$(chat_text "$session_d" "can you remind me what my codename is?")"
expect_contains "D remind me nested recall" "$response" "Moonrise"
chat_text "$session_d" "update my codename to Sunrise" >/dev/null
response="$(chat_text "$session_d" "what was my codename again?")"
expect_contains "D update-to recall" "$response" "Sunrise"
chat_text "$session_d" "set my project motto to steady sparks" >/dev/null
response="$(chat_text "$session_d" "do you know my project motto?")"
expect_contains "D do-you-know recall" "$response" "steady sparks"
codename_latest="$(sql "SELECT raw_value::text FROM observations WHERE subject_id='${subject_d}' AND attribute='codename' ORDER BY created_at DESC, id DESC LIMIT 1;")"
motto_latest="$(sql "SELECT raw_value::text FROM observations WHERE subject_id='${subject_d}' AND attribute='project_motto' ORDER BY created_at DESC, id DESC LIMIT 1;")"
printf 'INFO Scenario D db_codename=%s db_project_motto=%s\n\n' "$codename_latest" "$motto_latest"

archive_rows="$(sql "SELECT COUNT(*) FROM conversation_messages WHERE session_id IN ('${session_a}','${session_b}','${session_c}','${session_d}');")"
printf 'INFO archived_conversation_rows=%s\n' "$archive_rows"
printf '\nSUMMARY pass=%d fail=%d\n' "$pass_count" "$fail_count"

if (( fail_count > 0 )); then
  exit 1
fi
