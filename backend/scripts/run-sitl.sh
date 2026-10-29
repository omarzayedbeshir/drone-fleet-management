#!/usr/bin/env bash
# Launch an isolated ArduCopter simulation while preserving its EEPROM/boot count.
set -euo pipefail
backend_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
ardupilot_root="${1:-${ARDUPILOT_HOME:-}}"
if [[ -z "$ardupilot_root" ]]; then
    echo 'Usage: ./backend/scripts/run-sitl.sh /absolute/path/to/ardupilot' >&2
    exit 1
fi
ardupilot_root="$(cd -- "$ardupilot_root" && pwd)"
binary="$ardupilot_root/build/sitl/bin/arducopter"
if [[ ! -x "$binary" ]]; then
    echo "Missing $binary. Build SITL in your ArduPilot checkout with ./waf configure --board sitl and ./waf copter." >&2
    exit 1
fi
state_dir="${SITL_STATE_DIR:-$backend_dir/.local/sitl}"
mkdir -p -- "$state_dir"
cd -- "$state_dir"
echo "SITL state: $PWD; MAVLink TCP port: 15760 (adapter endpoint tcp://127.0.0.1:15760)"
exec "$binary" --model + --instance 20 --serial0 tcp:15760 \
    --home 30.0444,31.2357,100,0 --speedup 1 \
    --defaults "$ardupilot_root/Tools/autotest/default_params/copter.parm"
