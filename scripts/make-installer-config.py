#!/usr/bin/env python3
"""Make the bootc-image-builder configuration for the NidaOS installer.

The script puts installer/nida-plan.sh into installer/kickstart.ks and prints
a TOML file on the standard output.
"""

import json
import pathlib
import sys

MARKER = "@@NIDA_PLAN@@"


def main() -> int:
    root = pathlib.Path(__file__).resolve().parent.parent
    kickstart = (root / "installer" / "kickstart.ks").read_text(encoding="utf-8")
    plan = (root / "installer" / "nida-plan.sh").read_text(encoding="utf-8")

    if kickstart.count(MARKER) != 1:
        print(f"error: kickstart.ks must contain {MARKER} one time", file=sys.stderr)
        return 1
    if "\r" in kickstart or "\r" in plan:
        print("error: the installer files must have LF line ends", file=sys.stderr)
        return 1

    kickstart = kickstart.replace(MARKER, plan.rstrip("\n"))

    # A JSON string is also a correct TOML basic string.
    print("[customizations.installer.kickstart]")
    print("contents = " + json.dumps(kickstart))
    return 0


if __name__ == "__main__":
    sys.exit(main())
