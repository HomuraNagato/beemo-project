#!/usr/bin/env python3
from __future__ import annotations

import importlib.util
import pathlib
import sys
import unittest


ROOT = pathlib.Path(__file__).resolve().parent
MODULE_PATH = ROOT / "eve_listen_lib.py"
SPEC = importlib.util.spec_from_file_location("eve_listen_lib", MODULE_PATH)
MODULE = importlib.util.module_from_spec(SPEC)
assert SPEC and SPEC.loader
sys.modules[SPEC.name] = MODULE
SPEC.loader.exec_module(MODULE)


class WakePhraseTests(unittest.TestCase):
    def test_inline_wake_phrase_dispatches_command(self):
        decision = MODULE.decide_wake_action(
            "Hey Beemo, what time is it?",
            ["hey beemo"],
            armed_until=0.0,
            now=100.0,
        )
        self.assertEqual(decision.action, "dispatch")
        self.assertEqual(decision.command_text, "what time is it")

    def test_wake_phrase_only_arms_listener(self):
        decision = MODULE.decide_wake_action(
            "okay beemo",
            ["okay beemo"],
            armed_until=0.0,
            now=100.0,
        )
        self.assertEqual(decision.action, "arm")
        self.assertEqual(decision.command_text, "")

    def test_armed_listener_dispatches_next_utterance(self):
        decision = MODULE.decide_wake_action(
            "tell me the weather",
            ["hey beemo"],
            armed_until=150.0,
            now=100.0,
        )
        self.assertEqual(decision.action, "dispatch")
        self.assertEqual(decision.command_text, "tell me the weather")

    def test_unarmed_listener_ignores_non_wake_utterance(self):
        decision = MODULE.decide_wake_action(
            "tell me the weather",
            ["hey beemo"],
            armed_until=50.0,
            now=100.0,
        )
        self.assertEqual(decision.action, "ignore")

    def test_build_grpcurl_command_uses_container_when_present(self):
        cmd = MODULE.build_grpcurl_command(
            "{}",
            container_name="eve-orchestrator",
            grpcurl_bin="grpcurl",
            proto_path="/workspace/proto/agent.proto",
            orch_addr="localhost:5013",
        )
        self.assertEqual(cmd[:4], ["docker", "exec", "-i", "eve-orchestrator"])
        self.assertIn("grpcurl", cmd)


if __name__ == "__main__":
    unittest.main()
