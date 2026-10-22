"""Exercise recovery with fake Docker/USB tools; never touch real devices."""

import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest


FAKE = r'''
import os
from pathlib import Path
import sys
state = Path(os.environ["MOCK_STATE"])
name = Path(sys.argv[0]).name
a = sys.argv[1:]
with (state / "calls").open("a") as f:
    f.write(name + " " + " ".join(a) + "\n")
if name == "uname":
    print("Darwin")
elif name == "orb":
    if a == ["start"]:
        (state / "engine-down").unlink(missing_ok=True)
    elif a == ["usb", "list"]:
        if not (state / "device-missing").exists():
            print("03100000  2ca3:4006  BAIWANG Baiwang")
    elif a[:2] == ["usb", "info"]:
        print("Machine: " + ("default" if (state / "attached").exists() else "Not attached"))
    elif a[:2] == ["usb", "attach"]:
        (state / "attached").touch()
    elif a[:2] == ["usb", "detach"]:
        (state / "attached").unlink(missing_ok=True)
elif name == "docker":
    if a == ["info"]:
        sys.exit(1 if (state / "engine-down").exists() else 0)
    elif a == ["context", "show"]:
        print("orbstack")
    elif a[:2] == ["image", "inspect"]:
        sys.exit(1 if (state / "image-missing").exists() else 0)
    elif a[:1] == ["inspect"]:
        print("false" if (state / "container-stopped").exists() else "true")
    elif a[:1] == ["compose"]:
        a = a[3:]  # skip compose -f FILE
        if a[:1] == ["ps"]:
            if not (state / "container-missing").exists():
                print("test-container")
        elif a[:1] == ["build"]:
            sys.exit(1 if (state / "build-fails").exists() else 0)
        elif a[:1] in (["up"], ["restart"]):
            (state / "container-stopped").unlink(missing_ok=True)
            (state / "container-missing").unlink(missing_ok=True)
        elif a[:1] == ["down"]:
            (state / "container-missing").touch()
        elif a[:1] == ["exec"]:
            print('{"discovery_error":"bad","vendor_id":"2ca3"}' if (state / "bad-health").exists()
                  else '{"discovery_error":"","vendor_id":"2ca3"}')
'''


class RecoveryTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.root = Path(self.tmp.name)
        (self.root / "scripts").mkdir()
        shutil.copy(Path(__file__).with_name("docker.sh"), self.root / "scripts/docker.sh")
        self.state = self.root / "mock"
        self.state.mkdir()
        self.bin = self.root / "bin"
        self.bin.mkdir()
        for tool in ("docker", "orb", "uname"):
            p = self.bin / tool
            p.write_text("#!" + sys.executable + "\n" + FAKE)
            p.chmod(0o755)
        self.env = dict(os.environ, PATH=str(self.bin) + ":" + os.environ["PATH"], MOCK_STATE=str(self.state))
        self.env.pop("DJONEHUB_USB_ID", None)
        self.runtime = self.root / "dist/docker"

    def tearDown(self):
        self.tmp.cleanup()

    def flag(self, name):
        (self.state / name).touch()

    def run_command(self, name="recover", success=True):
        r = subprocess.run(["/bin/bash", str(self.root / "scripts/docker.sh"), name],
                           env=self.env, capture_output=True, text=True, timeout=15)
        self.assertEqual(r.returncode == 0, success, r.stdout + r.stderr)
        return r

    def calls(self):
        p = self.state / "calls"
        return p.read_text() if p.exists() else ""

    def test_reboot_recovers_engine_usb_and_container_without_build(self):
        self.flag("engine-down")
        self.flag("container-stopped")
        self.run_command()
        calls = self.calls()
        self.assertIn("orb start", calls)
        self.assertIn("orb usb attach 03100000", calls)
        self.assertIn("up -d --no-build --pull never", calls)
        self.assertNotIn("--force-recreate", calls)
        self.assertNotIn("build\n", calls)
        self.assertEqual((self.runtime / "usb-id").read_text().strip(), "03100000")

    def test_healthy_service_is_not_restarted_or_reattached(self):
        self.flag("attached")
        self.run_command()
        self.assertNotIn("orb usb attach", self.calls())
        self.assertNotIn(" restart ", self.calls())
        self.assertNotIn(" up ", self.calls())

    def test_manual_stop_pauses_recovery_until_manual_start(self):
        self.run_command("stop")
        before = self.calls()
        self.run_command()
        self.assertEqual(self.calls(), before)
        self.assertTrue((self.runtime / "paused").exists())
        self.run_command("up")
        self.assertFalse((self.runtime / "paused").exists())

    def test_missing_hardware_does_not_restart_container(self):
        self.flag("device-missing")
        self.run_command(success=False)
        self.assertNotIn(" up ", self.calls())
        self.assertNotIn(" restart ", self.calls())

    def test_bad_health_waits_before_restart_and_good_health_clears_count(self):
        self.flag("bad-health")
        self.run_command()
        self.run_command()
        self.assertNotIn(" restart ", self.calls())
        self.run_command()
        self.assertEqual(self.calls().count(" restart djonehub"), 1)
        self.run_command()
        (self.state / "bad-health").unlink()
        self.run_command()
        self.assertFalse((self.runtime / "recovery-failures").exists())

    def test_busy_manual_operation_is_skipped(self):
        lock = self.runtime / "operation.lock"
        lock.mkdir(parents=True)
        (lock / "pid").write_text(str(os.getpid()))
        self.run_command()
        self.assertEqual(self.calls(), "")

    def test_crashed_process_lock_is_reclaimed(self):
        lock = self.runtime / "operation.lock"
        lock.mkdir(parents=True)
        (lock / "pid").write_text("2147483647")
        self.run_command()
        self.assertFalse(lock.exists())

    def test_reused_pid_from_previous_boot_is_reclaimed(self):
        lock = self.runtime / "operation.lock"
        lock.mkdir(parents=True)
        (lock / "pid").write_text(str(os.getpid()))
        (lock / "started").write_text("previous boot")
        self.run_command()
        self.assertFalse(lock.exists())

    def test_failed_build_keeps_paused_state_and_existing_container(self):
        self.runtime.mkdir(parents=True)
        (self.runtime / "paused").touch()
        self.flag("build-fails")
        self.run_command("up", success=False)
        self.assertTrue((self.runtime / "paused").exists())
        self.assertNotIn(" down", self.calls())
        self.assertNotIn(" up ", self.calls())


if __name__ == "__main__":
    unittest.main()
