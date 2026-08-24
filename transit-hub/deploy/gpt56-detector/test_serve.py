import sqlite3
import unittest
from unittest.mock import patch

from serve import ResilientAppState


class ResilientStatusTest(unittest.TestCase):
    def test_sqlite_status_error_returns_recoverable_state(self) -> None:
        state = object.__new__(ResilientAppState)
        state.lock = __import__("threading").RLock()
        state.detector = {"status": "interrupted", "session_id": "stale"}
        state.generator = {"status": "idle"}

        with patch.object(ResilientAppState.__mro__[1], "safe_status", side_effect=sqlite3.OperationalError("unable to open database file")):
            result = state.safe_status("detector")

        self.assertEqual(result["status"], "interrupted")
        self.assertEqual(result["status_read_error"], "sqlite_unavailable")
        self.assertNotIn("progress", result)

    def test_running_sqlite_status_error_becomes_interrupted(self) -> None:
        state = object.__new__(ResilientAppState)
        state.lock = __import__("threading").RLock()
        state.detector = {"status": "running", "session_id": "stale"}
        state.generator = {"status": "idle"}

        with patch.object(ResilientAppState.__mro__[1], "safe_status", side_effect=sqlite3.OperationalError("database is locked")):
            result = state.safe_status("detector")

        self.assertEqual(result["status"], "interrupted")


if __name__ == "__main__":
    unittest.main()
