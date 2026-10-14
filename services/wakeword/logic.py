import re
from typing import Iterable, List, Optional


WORD_RE = re.compile(r"[a-z0-9]+")
QUESTION_RE = re.compile(r"\?\s*$")
SENTENCE_BOUNDARY_RE = re.compile(r"(?<=[.!?])(?:\s+|$)")
FENCED_CODE_RE = re.compile(r"```.*?```", re.DOTALL)
MARKDOWN_LINK_RE = re.compile(r"\[([^\]]+)\]\([^\)]+\)")
SOURCE_APPENDIX_RE = re.compile(r"(?ims)\n\s*(?:#{1,6}\s*)?sources\s*:\s*\n.*$")
CITATION_RE = re.compile(r"\s*\[\d+(?:\s*,\s*\d+)*\]")
LEADING_EXPRESSION_RE = re.compile(r"^\s*\[(?:calm|sad|worried|surprised|listening|thinking|hidden)\]\s*", re.IGNORECASE)
LEADING_WAKE_NOISE_RE = re.compile(
    r"^\s*(?:(?:hey|hay|hi|okay|ok|k|a)\W+)?"
    r"(?:bee\W*mo+h?|be+mo+h?|b+mo+h?|b\W*mole?h?|bimo|beemo|beemoh|bmo|pmo|pimo)"
    r"\b[\s,.:;!?-]*",
    re.IGNORECASE,
)
LEADING_HEY_CLAUSE_RE = re.compile(r"^\s*(?:hey|hay|hi|okay|ok)\b[^,.:;!?]{0,24}[,.:;!?]\s*", re.IGNORECASE)


def split_phrases(raw: str) -> List[str]:
    phrases = [part.strip() for part in raw.split(",") if part.strip()]
    return phrases or ["hey beemo", "hey bmo", "okay beemo", "ok beemo"]


def split_optional_phrases(raw: str) -> List[str]:
    return [part.strip() for part in raw.split(",") if part.strip()]


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


def strip_leading_wake_noise(transcript: str) -> str:
    text = transcript.strip()
    for _ in range(3):
        cleaned = LEADING_WAKE_NOISE_RE.sub("", text, count=1).strip()
        cleaned = LEADING_HEY_CLAUSE_RE.sub("", cleaned, count=1).strip()
        if cleaned == text:
            break
        text = cleaned
    if text.lower().startswith("but time is it"):
        return "what time is it" + text[len("but time is it") :]
    return text


def should_listen_for_followup(response: str) -> bool:
    return bool(QUESTION_RE.search(response.strip()))


def take_complete_sentences(buffer: str) -> tuple[List[str], str]:
    matches = list(SENTENCE_BOUNDARY_RE.finditer(buffer))
    if not matches:
        return [], buffer
    cutoff = matches[-1].end()
    complete = buffer[:cutoff].strip()
    remainder = buffer[cutoff:]
    return [part.strip() for part in SENTENCE_BOUNDARY_RE.split(complete) if part.strip()], remainder


def prepare_tts_text(response: str) -> str:
    text = LEADING_EXPRESSION_RE.sub("", response.strip())
    text = SOURCE_APPENDIX_RE.sub("", text)
    text = CITATION_RE.sub("", text)
    text = FENCED_CODE_RE.sub(" I included code in the text response. ", text)
    text = MARKDOWN_LINK_RE.sub(r"\1", text)
    text = re.sub(r"`([^`]+)`", r"\1", text)
    text = re.sub(r"(?m)^\s{0,3}(?:#{1,6}\s+|[-*+]\s+|\d+[.)]\s+)", "", text)
    text = re.sub(r"[*_~]", "", text)
    return re.sub(r"\s+", " ", text).strip()
