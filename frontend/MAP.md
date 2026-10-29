# Fleet map and telemetry

In the registry, select **Show map**. The map follows the current registry page and applied filters, not every aircraft in the organisation. Registry pagination changes the aircraft shown. Fleet managers, pilots, and maintenance technicians can read their own organisation's telemetry through the existing authenticated history endpoint.

## Monitoring

Start the API, frontend and the drone's adapter using the [SITL walkthrough](../docs/SITL_DEMO.md). Select an aircraft from the **Aircraft** list or click its marker. The panel displays reported status, battery percentage, MSL altitude, speed, lifetime flight hours, battery cycles, fault flags and observation timestamps.

**Refresh telemetry** reloads a snapshot. Automatic refresh runs every five seconds while the map is open and moves markers to newly reported positions. There is no toggle to disable it. It avoids overlapping refreshes, pauses network updates in hidden tabs, and stops when the map closes or unmounts. The map, markers, trails, and current readings stay mounted while refreshes run. Coordinates and trail geometry update in place without resetting pan/zoom or shifting the map for a loading banner. A failed refresh retains the last loaded readings and displays an error with their original timestamps. The five-second delay starts after each refresh completes. Authenticated live requests keep the session active while monitoring. This is polling, not a WebSocket feed or a connector-health service. The registry table has its own Refresh button; map refresh updates only the map panel.

**Fit aircraft** fits known positions; **Locate selected** centres the selected GPS point. Selecting an aircraft also works through the keyboard-accessible list, without clicking the map.

## Recent trails

At most 100 recent observations per aircraft are loaded through `GET /api/v1/drones/{id}/telemetry?limit=100&offset=0`, with at most four concurrent requests. A partial failure is shown per aircraft rather than represented as a connected drone. Older history may exist outside the loaded window.

The map always displays the latest loaded readings and GPS positions. Recent trails show the selected aircraft's stored movement; there are no playback controls or position slider.

GPS loss, gaps over 30 seconds, and date-line crossings break the line. Drones without GPS are not placed at a default coordinate. If a new observation lacks GPS, the map retains the last GPS point in the loaded window with its own timestamp, while other readings come from the latest observation. Observation and GPS timestamps indicate the age of the readings; recent readings do not prove connection health. Unknown values remain “Not reported”; zero remains a valid value.

Reported status is separate from the registry's grounded projection and cannot clear a maintenance restriction. Battery cycles are stored totals; the SITL adapter does not measure new cycles. Stationary SITL will stay stationary. Movement requires changing coordinates from the simulator/source; the UI sends no flight-control commands.

## Basemap and verification

Leaflet 1.9.4 renders OpenStreetMap tiles with visible attribution. Opening the map requests the viewed geographic area from `tile.openstreetmap.org`; telemetry records and credentials are not sent to that provider. There is no prefetch or offline tile download. A tile failure is reported while coordinates and telemetry details remain usable. Review the [OpenStreetMap tile policy](https://operations.osmfoundation.org/policies/tiles/) before scaling; the public service has no availability guarantee.

Run `npm test` and `npm run build` from `frontend/`. Tests cover valid zero coordinates, missing/invalid GPS, chronological trails and gaps, timestamps and unknown readings, partial failures, automatic live polling and cleanup. Component tests mock the map renderer. Browser checks separately exercise Leaflet with isolated synthetic telemetry, not a physical flight.
