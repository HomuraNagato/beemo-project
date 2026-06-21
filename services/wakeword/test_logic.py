import unittest

from services.wakeword.logic import (
    extract_prompt,
    should_listen_for_followup,
    split_optional_phrases,
    split_phrases,
    strip_leading_wake_noise,
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


if __name__ == "__main__":
    unittest.main()
