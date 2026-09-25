"""Parse the Go-rendered node seed without executing any installer command."""
import base64
import json
from pathlib import Path
import struct
import subprocess
import tempfile
import unittest

import yaml

REPO = Path(__file__).resolve().parents[2]


class ProvisioningProfile(unittest.TestCase):
    def test_dual_nic_and_sudo_argv_survive_yaml_decoding(self):
        with tempfile.TemporaryDirectory(prefix="labfleet-seed-test-") as tmp:
            root = Path(tmp)
            key_type = b"ssh-ed25519"
            wire = struct.pack("!I", len(key_type)) + key_type + struct.pack("!I", 32) + bytes(range(32))
            key = root / "synthetic.pub"
            key.write_text("ssh-ed25519 " + base64.b64encode(wire).decode() + " synthetic-test\n")
            config = json.loads((REPO / "provisioning/config.example.json").read_text())
            config.update(management_mac="02:00:00:00:00:09", passwordless_sudo=True,
                          ssh_key_path=str(key), output=str(root / "rendered"))
            path = root / "config.json"
            path.write_text(json.dumps(config))
            subprocess.run(["go", "run", "./provisioning/cmd/provisionctl", "render", "--config", str(path)],
                           cwd=REPO, check=True, capture_output=True, timeout=60)
            seed = yaml.safe_load((root / "rendered/user-data").read_text())["autoinstall"]
            nics = seed["network"]["ethernets"]
            self.assertFalse(nics["prov0"]["dhcp4-overrides"]["use-routes"])
            self.assertFalse(nics["prov0"]["dhcp4-overrides"]["use-dns"])
            self.assertTrue(nics["mgmt0"]["dhcp4"])
            self.assertIn("qemu-guest-agent", seed["packages"])
            command = seed["late-commands"][-1]
            self.assertEqual(command[:6], ["curtin", "in-target", "--target=/target", "--", "sh", "-c"])
            self.assertIn(" && ", command[6])
            self.assertNotIn("\\u0026", command[6])
            self.assertIn("NOPASSWD: ALL", command[6])
            self.assertIn("visudo -cf", command[6])
            self.assertFalse(seed["ssh"]["allow-pw"])
            self.assertTrue(seed["user-data"]["disable_root"])


if __name__ == "__main__":
    unittest.main()
