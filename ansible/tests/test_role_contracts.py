"""Exercise actual role assertions and the TOML template without configuring hosts."""
import copy
import os
from pathlib import Path
import subprocess
import tempfile
import tomllib
import unittest

import yaml

ROOT = Path(__file__).resolve().parents[1]


class RoleContracts(unittest.TestCase):
    def assertion(self, role, name, variables):
        tasks = yaml.safe_load((ROOT / f"roles/{role}/tasks/main.yml").read_text())
        task = copy.deepcopy(next(t for t in tasks if t["name"] == name))
        self.assertIn("ansible.builtin.assert", task)
        task.pop("when", None)
        with tempfile.TemporaryDirectory(prefix="labfleet-role-test-") as tmp:
            play = Path(tmp) / "assert.yml"
            play.write_text(yaml.safe_dump([{
                "name": "Controller-only assertion fixture", "hosts": "localhost",
                "gather_facts": False, "vars": variables, "tasks": [task],
            }]))
            result = subprocess.run(
                ["ansible-playbook", "-i", "localhost,", str(play)],
                cwd=ROOT, env=dict(os.environ, ANSIBLE_CONFIG=str(ROOT / "ansible.cfg")),
                capture_output=True, text=True, timeout=20,
            )
            return result.returncode, result.stdout + result.stderr

    def test_containerd_version_guard(self):
        for version, accepted in [("1.7.24", True), ("1.7.28", True), ("2.0.1", False), ("1.6.36", False)]:
            with self.subTest(version=version):
                rc, out = self.assertion("containerd", "Require containerd 1.7 before writing v2 configuration", {
                    "containerd_containerd_version": {"stdout": f"containerd github.com/containerd/containerd {version} example"},
                })
                self.assertEqual(rc == 0, accepted, out)

    def test_effective_ssh_policy_rejects_conflicts(self):
        policy = ["pubkeyauthentication yes", "passwordauthentication no", "kbdinteractiveauthentication no", "permitrootlogin no"]
        for index in [None, 0, 1, 2, 3]:
            lines = policy.copy()
            if index is not None:
                lines[index] = lines[index].rsplit(" ", 1)[0] + " conflicting-value"
            rc, out = self.assertion("ssh", "Require effective key-only non-root SSH policy", {
                "ssh_effective_policy": {"stdout_lines": lines},
            })
            self.assertEqual(rc == 0, index is None, out)

    def test_containerd_template_enables_cri_and_systemd_cgroups(self):
        config = tomllib.loads((ROOT / "roles/containerd/templates/config.toml.j2").read_text())
        self.assertEqual(config["version"], 2)
        self.assertNotIn("cri", config["disabled_plugins"])
        runtime = config["plugins"]["io.containerd.grpc.v1.cri"]["containerd"]["runtimes"]["runc"]
        self.assertEqual(runtime["runtime_type"], "io.containerd.runc.v2")
        self.assertTrue(runtime["options"]["SystemdCgroup"])


if __name__ == "__main__":
    unittest.main()
