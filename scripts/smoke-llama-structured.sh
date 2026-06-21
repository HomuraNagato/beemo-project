#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'USAGE'
usage: smoke-llama-structured.sh [--url URL] [--model NAME] [--max-tokens N]

Runs live llama.cpp structured-output smoke tests against /completion.
Default URL: http://127.0.0.1:5014/completion
USAGE
}

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
URL="${LLAMA_COMPLETION_URL:-http://127.0.0.1:5014/completion}"
MODEL="${LLAMA_MODEL:-}"
MAX_TOKENS="${LLAMA_SMOKE_MAX_TOKENS:-256}"

while [ "$#" -gt 0 ]; do
  case "$1" in
    --url)
      URL="$2"
      shift 2
      ;;
    --model)
      MODEL="$2"
      shift 2
      ;;
    --max-tokens)
      MAX_TOKENS="$2"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      usage >&2
      printf 'unknown argument: %s\n' "$1" >&2
      exit 2
      ;;
  esac
done

python3 - "$ROOT_DIR" "$URL" "$MODEL" "$MAX_TOKENS" <<'PY'
from __future__ import annotations

import json
import sys
import time
from pathlib import Path
from urllib.error import URLError
from urllib.request import Request, urlopen


root = Path(sys.argv[1])
url = sys.argv[2]
model = sys.argv[3]
max_tokens = int(sys.argv[4])


def normalize_llamacpp_grammar(grammar: str) -> str:
    out: list[str] = []
    in_string = False
    in_char_class = False
    escaped = False
    for ch in grammar:
        if in_string:
            out.append(ch)
            if escaped:
                escaped = False
            elif ch == "\\":
                escaped = True
            elif ch == '"':
                in_string = False
            continue
        if in_char_class:
            out.append(ch)
            if escaped:
                escaped = False
            elif ch == "\\":
                escaped = True
            elif ch == "]":
                in_char_class = False
            continue
        if ch == '"':
            in_string = True
            out.append(ch)
        elif ch == "[":
            in_char_class = True
            out.append(ch)
        elif ch == "_":
            out.append("-")
        else:
            out.append(ch)
    return "".join(out)


def require_single_tool(grammar: str) -> str:
    return grammar.replace(
        "root ::= empty_list | single_call_list",
        "root ::= single_call_list",
        1,
    )


def restrict_tool(grammar: str, tool_rule: str, calc_rule: str | None = None) -> str:
    grammar = require_single_tool(grammar)
    grammar = grammar.replace(
        "tool_call ::= get_time_call | weather_call | older_sister_call | calculator_call | beemo_direct_call",
        f"tool_call ::= {tool_rule}",
        1,
    )
    if calc_rule:
        grammar = grammar.replace(
            "calc_args ::= expression_args | convert_args | bmi_args | bmr_args | tdee_args | percent_of_args | percent_change_args | percent_ratio_args",
            f"calc_args ::= {calc_rule}",
            1,
        )
    return grammar


def request_completion(prompt: str, grammar: str) -> tuple[str, dict, float]:
    payload = {
        "prompt": prompt,
        "grammar": normalize_llamacpp_grammar(grammar),
        "n_predict": max_tokens,
        "temperature": 0,
        "stream": False,
    }
    if model:
        payload["model"] = model
    req = Request(
        url,
        data=json.dumps(payload).encode("utf-8"),
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    start = time.monotonic()
    with urlopen(req, timeout=180) as resp:
        body = json.loads(resp.read().decode("utf-8"))
    elapsed_ms = (time.monotonic() - start) * 1000
    content = body.get("content", "")
    if not content and body.get("choices"):
        first = body["choices"][0]
        content = first.get("text") or first.get("message", {}).get("content", "")
    return str(content).strip(), body, elapsed_ms


tool_grammar = (root / "scripts/grammars/tool_list.gbnf").read_text(encoding="utf-8")
route_grammar = '''root ::= "{" ws "\\"route_id\\"" ws ":" ws route_id ws "}"
route_id ::= "\\"" route_id_chars "\\""
route_id_chars ::= route_id_char+
route_id_char ::= [a-zA-Z0-9_.-]
ws ::= ""'''

cases = [
    {
        "name": "min_array",
        "prompt": 'Return ["ok"] exactly.',
        "grammar": 'root ::= "[\\"ok\\"]"',
        "check": lambda value: value == ["ok"],
    },
    {
        "name": "route_time",
        "prompt": """Choose exactly one route_id from the candidate routes. Return JSON object only: {"route_id":"..."}.
Rules:
- Choose intent only. Do not write tool arguments.

Candidate routes:
Candidate 1
- route_id: get_time.current_or_relative
- similarity: 0.900
- tool: get_time
- default_args: {}
- summary: Current time or date.

Active conversation thread:
(none)
User query: What is today's date?
Route decision:""",
        "grammar": route_grammar,
        "check": lambda value: isinstance(value, dict) and value.get("route_id") == "get_time.current_or_relative",
    },
    {
        "name": "route_convert_height",
        "prompt": """Choose exactly one route_id from the candidate routes. Return JSON object only: {"route_id":"..."}.
Rules:
- Choose intent only. Do not write tool arguments.

Candidate routes:
Candidate 1
- route_id: calculator.convert
- domain_id: calculator
- similarity: 0.755
- tool: calculator
- default_args: {"operation":"convert"}
- required_fields: input_or_value, to_unit
- summary: Convert between units, including compound units such as speed, pace, and chemistry concentration.
- example: convert 5 foot 4 to centimeters
Candidate 2
- route_id: calculator.bmi
- domain_id: calculator
- similarity: 0.657
- tool: calculator
- default_args: {"operation":"bmi"}
- summary: Calculate BMI from weight and height.
Candidate 3
- route_id: beemo.direct
- domain_id: beemo
- similarity: 0.580
- tool: beemo.direct
- default_args: {}
- summary: Answer directly.

Active conversation thread:
(none)
User query: What is 5 foot 4 inches in centimeters?
Route decision:""",
        "grammar": route_grammar,
        "check": lambda value: isinstance(value, dict) and value.get("route_id") == "calculator.convert",
    },
    {
        "name": "extract_convert_height",
        "prompt": """Generate the single tool call for the selected route. Return JSON array only.
Rules:
- Use only the selected route.
- Preserve default_args exactly.
- Copy explicit measurements exactly; do not convert, duplicate, infer, or invent measurements.
- Omit missing fields rather than guessing.
- Do not answer the user.

Selected route:
Candidate 1
- route_id: calculator.convert
- domain_id: calculator
- similarity: 0.755
- tool: calculator
- default_args: {"operation":"convert"}
- required_fields: input_or_value, to_unit
- summary: Convert between units, including compound units such as speed, pace, and chemistry concentration.
- example: convert 5 foot 4 to centimeters

Active conversation thread:
(none)
User query: What is 5 foot 4 inches in centimeters?
Tool calls:""",
        "grammar": restrict_tool(tool_grammar, "calculator_call", "convert_args"),
        "check": lambda value: check_convert_height(value),
    },
]


def check_convert_height(value: object) -> bool:
    if not isinstance(value, list) or len(value) != 1:
        return False
    call = value[0]
    if not isinstance(call, dict) or call.get("tool") != "calculator":
        return False
    args = call.get("args")
    if not isinstance(args, dict):
        return False
    if args.get("operation") != "convert" or args.get("to_unit") != "cm":
        return False
    components = args.get("input")
    if not isinstance(components, list):
        return False
    seen = {(item.get("unit"), item.get("value")) for item in components if isinstance(item, dict)}
    return ("ft", 5) in seen and ("in", 4) in seen


def parse_json_output(content: str) -> tuple[object | None, str]:
    try:
        return json.loads(content), ""
    except json.JSONDecodeError as exc:
        return None, str(exc)


failures = 0
print(f"llama structured smoke url={url} max_tokens={max_tokens}")
for case in cases:
    try:
        content, body, elapsed_ms = request_completion(case["prompt"], case["grammar"])
    except (OSError, URLError, TimeoutError) as exc:
        failures += 1
        print(f"FAIL {case['name']} request_error={exc}")
        continue

    parsed, parse_error = parse_json_output(content)
    ok = not parse_error and case["check"](parsed)
    status = "PASS" if ok else "FAIL"
    if not ok:
        failures += 1
    token_note = ""
    if body.get("timings", {}).get("predicted_n") == max_tokens:
        token_note = " hit_max_tokens=true"
    print(f"{status} {case['name']} {elapsed_ms:.0f}ms{token_note}")
    if not ok:
        if parse_error:
            print(f"  parse_error: {parse_error}")
        print(f"  raw: {content[:1200]!r}")

if failures:
    raise SystemExit(1)
PY
