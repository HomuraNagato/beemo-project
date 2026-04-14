import re
from typing import Iterable, List, Optional


WORD_RE = re.compile(r"[a-z0-9]+")
QUESTION_RE = re.compile(r"\?\s*$")


def split_phrases(raw: str) -> List[str]:
    phrases = [part.strip() for part in raw.split(",") if part.strip()]
    return phrases or ["hey beemo", "hey bmo", "okay beemo", "ok beemo"]


def compile_phrase_patterns(phrases: Iterable[str]) -> List[re.Pattern[str]]:
    patterns = []
    for phrase in phrases:
        tokens = WORD_RE.findall(phrase.lower())
        if not tokens:
            continue
        patterns.append(re.compile(r"\b" + r"\W+".join(re.escape(token) for token in tokens) + r"\b", re.IGNORECASE))
    return patterns


def extract_prompt(transcript: str, phrases: Iterable[str]) -> Optional[str]:
    text = transcript.strip()
    if not text:
        return None

    patterns = compile_phrase_patterns(phrases)
    for pattern in patterns:
        match = pattern.search(text)
        if not match:
            continue
        prefix_tokens = WORD_RE.findall(text[:match.start()].lower())
        if len(prefix_tokens) > 2:
            continue
        remainder = text[match.end() :].lstrip(" ,.:;!?-")
        return remainder.strip()
    return None


def should_listen_for_followup(response: str) -> bool:
    return bool(QUESTION_RE.search(response.strip()))
