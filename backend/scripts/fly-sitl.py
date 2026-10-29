#!/usr/bin/env python3
"""Fly the local run-sitl.sh simulation; never accepts a physical vehicle endpoint."""
import argparse
import math
import os
from pathlib import Path
import time


def require_local_sitl():
    # Verify the control listener belongs to the exact isolated simulator setup.
    inodes = set()
    for line in Path('/proc/net/tcp').read_text().splitlines()[1:]:
        fields = line.split()
        if int(fields[1].split(':')[1], 16) == 5962 and fields[3] == '0A':
            inodes.add(fields[9])
    for proc in Path('/proc').iterdir():
        if not proc.name.isdigit():
            continue
        try:
            args = (proc / 'cmdline').read_bytes().decode().split('\0')
            if not args or not args[0].endswith('/build/sitl/bin/arducopter'):
                continue
            if '--instance' not in args or args[args.index('--instance') + 1] != '20':
                continue
            if '--serial0' not in args or args[args.index('--serial0') + 1] != 'tcp:15760':
                continue
            for fd in (proc / 'fd').iterdir():
                if os.readlink(fd) in {f'socket:[{inode}]' for inode in inodes}:
                    return
        except (OSError, UnicodeError, IndexError):
            continue
    raise RuntimeError('Refusing flight: port 5962 is not owned by the run-sitl.sh ArduCopter instance 20')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--laps', type=int, default=2, choices=range(1, 6), help='Square laps (1–5, default 2)')
    args = parser.parse_args()
    require_local_sitl()
    try:
        from pymavlink import mavutil
    except ImportError as error:
        raise RuntimeError('Install pymavlink in your Python environment: python3 -m pip install pymavlink') from error

    mav = mavutil.mavlink
    link = mavutil.mavlink_connection('tcp:127.0.0.1:5962', source_system=255, source_component=191)
    last_heartbeat = 0.0
    recent_status = []
    system, component = 1, 1

    def receive(timeout=1):
        nonlocal last_heartbeat
        if time.monotonic() - last_heartbeat >= 1:
            link.mav.heartbeat_send(mav.MAV_TYPE_GCS, mav.MAV_AUTOPILOT_INVALID, 0, 0, mav.MAV_STATE_ACTIVE)
            last_heartbeat = time.monotonic()
        msg = link.recv_match(blocking=True, timeout=timeout)
        if msg and msg.get_srcSystem() == system and msg.get_srcComponent() == component:
            if msg.get_type() == 'STATUSTEXT':
                recent_status.append(str(msg.text))
                del recent_status[:-8]
            return msg
        return None

    def wait(kind, predicate, timeout, description):
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            msg = receive()
            if msg and msg.get_type() == kind and predicate(msg):
                return msg
        raise RuntimeError(f'Timed out: {description}. Autopilot: {"; ".join(recent_status)}')

    def command(command_id, *params):
        link.mav.command_long_send(system, component, command_id, 0, *(list(params) + [0] * (7 - len(params))))
        ack = wait('COMMAND_ACK', lambda msg: msg.command == command_id, 10, f'command acknowledgement {command_id}')
        if ack.result not in (mav.MAV_RESULT_ACCEPTED, mav.MAV_RESULT_IN_PROGRESS):
            raise RuntimeError(f'Autopilot rejected command {command_id}: result {ack.result}; {"; ".join(recent_status)}')

    def mode(value):
        link.mav.set_mode_send(system, mav.MAV_MODE_FLAG_CUSTOM_MODE_ENABLED, value)
        wait('HEARTBEAT', lambda msg: msg.custom_mode == value, 12, f'flight mode {value}')

    def land():
        print('Landing simulated aircraft…', flush=True)
        mode(9)  # LAND. Do not force disarm in the air.
        wait('HEARTBEAT', lambda msg: not msg.base_mode & mav.MAV_MODE_FLAG_SAFETY_ARMED, 90, 'landing and automatic disarm')
        print('Landed and disarmed.', flush=True)

    armed_here = False
    try:
        heartbeat = wait('HEARTBEAT', lambda msg: msg.autopilot == mav.MAV_AUTOPILOT_ARDUPILOTMEGA and msg.type != mav.MAV_TYPE_GCS, 15, 'ArduPilot heartbeat')
        if heartbeat.base_mode & mav.MAV_MODE_FLAG_SAFETY_ARMED:
            raise RuntimeError('Simulator already armed; finish the existing flight before running this demo')
        link.mav.request_data_stream_send(system, component, mav.MAV_DATA_STREAM_ALL, 4, 1)
        wait('GPS_RAW_INT', lambda msg: msg.fix_type >= 3, 30, '3D GPS fix')
        origin = wait('GLOBAL_POSITION_INT', lambda msg: abs(msg.relative_alt) < 2000, 15, 'ground position')
        lat, lon = origin.lat / 1e7, origin.lon / 1e7
        print(f'SITL-only demo: {args.laps} × 60 m square, 12 m above home. Start {lat:.6f}, {lon:.6f}', flush=True)
        mode(4)  # GUIDED
        command(mav.MAV_CMD_COMPONENT_ARM_DISARM, 1)
        armed_here = True
        wait('HEARTBEAT', lambda msg: bool(msg.base_mode & mav.MAV_MODE_FLAG_SAFETY_ARMED), 12, 'normal arming')
        command(mav.MAV_CMD_NAV_TAKEOFF, 0, 0, 0, 0, 0, 0, 12)
        wait('GLOBAL_POSITION_INT', lambda msg: msg.relative_alt >= 10_500, 45, 'takeoff to 12 metres')
        command(mav.MAV_CMD_DO_CHANGE_SPEED, 1, 4, -1)
        for lap in range(args.laps):
            for number, (north, east) in enumerate(((60, 0), (60, 60), (0, 60), (0, 0)), 1):
                target_lat = lat + north / 111320
                target_lon = lon + east / (111320 * math.cos(math.radians(lat)))
                print(f'Lap {lap + 1}/{args.laps}, waypoint {number}/4: {north} m north, {east} m east', flush=True)
                link.mav.set_position_target_global_int_send(0, system, component, mav.MAV_FRAME_GLOBAL_RELATIVE_ALT_INT, 3576, round(target_lat * 1e7), round(target_lon * 1e7), 12, 0, 0, 0, 0, 0, 0, 0, 0)

                def reached(msg):
                    delta_n = (msg.lat / 1e7 - target_lat) * 111320
                    delta_e = (msg.lon / 1e7 - target_lon) * 111320 * math.cos(math.radians(lat))
                    return math.hypot(delta_n, delta_e) < 2.5

                position = wait('GLOBAL_POSITION_INT', reached, 45, 'waypoint arrival')
                print(f'  Reached {position.lat / 1e7:.6f}, {position.lon / 1e7:.6f}; altitude {position.relative_alt / 1000:.1f} m', flush=True)
        land()
        armed_here = False
    finally:
        try:
            if armed_here:
                land()
        finally:
            link.close()


if __name__ == '__main__':
    try:
        main()
    except (RuntimeError, OSError) as error:
        raise SystemExit(str(error))
