import unittest

from services.wakeword.logic import (
    extract_prompt,
    prepare_tts_text,
    should_listen_for_followup,
    split_optional_phrases,
    split_phrases,
    strip_leading_wake_noise,
    take_complete_sentences,
)


class WakewordLogicTest(unittest.TestCase):
    def test_split_phrases_uses_defaults(self):
        self.assertEqual(
            split_phrases(""),
            ["hey beemo", "hey bmo", "okay beemo", "ok beemo"],
        )

    def test_extract_prompt_strips_wake_phrase(self):
        phrases = split_phrases("hey beemo,ok beemo")
        self.assertEqual(extract_prompt("hey beemo what time is it", phrases), "what time is it")

    def test_extract_prompt_handles_punctuation(self):
        phrases = split_phrases("hey beemo")
        self.assertEqual(extract_prompt("Hey Beemo, tell me a joke", phrases), "tell me a joke")

    def test_extract_prompt_requires_phrase_near_front(self):
        phrases = split_phrases("hey beemo")
        self.assertIsNone(extract_prompt("I heard someone say hey beemo yesterday", phrases))

    def test_extract_prompt_returns_empty_string_for_phrase_only(self):
        phrases = split_phrases("hey beemo")
        self.assertEqual(extract_prompt("hey beemo", phrases), "")

    def test_extract_prompt_accepts_asr_aliases(self):
        phrases = split_phrases("hey beemo") + split_optional_phrases("don't be mad,dont be mad")
        self.assertEqual(extract_prompt("Don't be mad. What time is it?", phrases), "What time is it?")

    def test_strip_leading_wake_noise_handles_common_asr_mishears(self):
        self.assertEqual(strip_leading_wake_noise("hey beemoh what time is it"), "what time is it")
        self.assertEqual(strip_leading_wake_noise("BMO, tell me a joke"), "tell me a joke")
        self.assertEqual(strip_leading_wake_noise("Hey PMO, what time is it?"), "what time is it?")
        self.assertEqual(strip_leading_wake_noise("Hey people, what time is it?"), "what time is it?")
        self.assertEqual(strip_leading_wake_noise("Hey, B-Mole. But time is it."), "what time is it.")

    def test_should_listen_for_followup_when_response_is_question(self):
        self.assertTrue(should_listen_for_followup("What is the height?"))

    def test_should_not_listen_for_followup_when_response_is_statement(self):
        self.assertFalse(should_listen_for_followup("The BMI is 17.15."))

    def test_prepare_tts_text_removes_visual_markup(self):
        response = "[calm] **Result:** see [the docs](https://example.test). `done`"
        self.assertEqual(prepare_tts_text(response), "Result: see the docs. done")

    def test_prepare_tts_text_does_not_read_fenced_code(self):
        response = "Use this:\n```sh\necho hello\n```\nThen continue."
        self.assertEqual(prepare_tts_text(response), "Use this: I included code in the text response. Then continue.")

    def test_prepare_tts_text_omits_citations_and_source_appendix(self):
        response = (
            "South Korea's exports increased [1], led by semiconductors [2, 3].\n\n"
            "Sources:\n"
            "[1] Export report: https://example.test/export\n"
            "[2] Semiconductor report: https://example.test/chips"
        )
        self.assertEqual(
            prepare_tts_text(response),
            "South Korea's exports increased, led by semiconductors.",
        )

    def test_take_complete_sentences_keeps_partial_tail(self):
        sentences, remainder = take_complete_sentences("First sentence. Second is par")
        self.assertEqual(sentences, ["First sentence."])
        self.assertEqual(remainder, "Second is par")

    def test_take_complete_sentences_flushes_terminal_sentence(self):
        sentences, remainder = take_complete_sentences("Finished now!")
        self.assertEqual(sentences, ["Finished now!"])
        self.assertEqual(remainder, "")


if __name__ == "__main__":
    unittest.main()
