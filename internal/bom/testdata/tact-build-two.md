# BOM — build two (soldered, 22 frets)

Build two is **the same instrument**. What changes is the brain.

## Target configuration

| | Build 1 (breadboard, today) | Build 2 (soldered) |
| --- | --- | --- |
| Frets | 11 (1-10, 12) | **22** |
| Piezo channels | 6 | 6 |
| ADC pins used | 18 of 18 | **10 of 18** |

## Proposed pin plan

| Function | Pins | Kind |
| --- | --- | --- |
| 6 string drives | 2-7 | digital |
| 3 mux commons | 14, 15, 16 | ADC (A0-A2) |

## Parts

| Part | Qty | Purpose | On hand | **Buy** |
| --- | --- | --- | --- | --- |
| Teensy 4.1 | 1 | the brain | 0 | **1** |
| CD74HC4051E | 3 | 22 frets over 24 mux channels | 3 | — |
| MCP6002-I/P | 3 | 6 piezo unity buffers | 0 | **3** |
| 1N4148 | 22 | one per fret, ADC ESD | 14 | **8+** |
| 1N4728A 3.3V zener | 6 | piezo clamps | 4 | **2+** |
| 4.7k | 22 | fret pulldowns | ~16 (kit) | **~6** |
| 220R | 6 | string drive series | kit | — |
| 100nF | 6 | decoupling, one per IC | 10 | — |
| DIP-8 socket | 3 | for the MCP6002s | 0 | **3** |
| DIP-16 socket | 3 | for the muxes | 0 | **3** |
| Adafruit Perma-Proto half-size (1609) | 3 | one per fret module | 0 | **3** |
| Adafruit Perma-Proto full-size (1606) | 1-2 | main board, 60 rows | 0 | **1-2** |
| ELEGOO 32pc perfboard kit | 1 | scratch + small sub-boards | on order | — |
| Pin headers | — | socket the Teensy | 0 | **1 set** |

Socket every IC.
