import unittest

from services.wakeword.logic import extract_prompt, should_listen_for_followup, split_phrases


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

    def test_should_listen_for_followup_when_response_is_question(self):
        self.assertTrue(should_listen_for_followup("What is the height?"))

    def test_should_not_listen_for_followup_when_response_is_statement(self):
        self.assertFalse(should_listen_for_followup("The BMI is 17.15."))


if __name__ == "__main__":
    unittest.main()
