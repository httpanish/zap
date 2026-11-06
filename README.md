# zap

A visual internet speed test for the terminal. `zap` measures download speed, upload speed, latency, jitter, and latency under load against Cloudflare's speed test endpoints, and shows them in a live animated view.

It is a single binary with no runtime dependencies, and it needs no API key or account.

## Install

```
go install github.com/httpanish/zap/cmd/zap@latest
```

Prebuilt binaries for Linux, macOS, and Windows are on the [releases page](https://github.com/httpanish/zap/releases).

## Usage

```
zap                 Run a speed test
zap history         Show past results
zap version         Print version information
```

### `zap` flags

| Flag | Default | Behaviour |
| --- | --- | --- |
| `--duration <d>` | `10s` | Length of each of the download and upload phases, from `3s` to `60s`. |
| `--streams <n>` | `4` | Parallel connections per transfer phase, from `1` to `32`. |
| `--json` | off | Print the result as JSON to stdout, with no live view. |
| `--card` | off | Write a 1200×630 PNG result card. |
| `--card-dir <dir>` | current directory | Directory for the card. Setting this also turns on `--card`. |
| `--no-save` | off | Don't add this run to history. |

Press `q`, `Esc`, or `Ctrl-C` to cancel a test. Cancelled runs aren't saved.

When stdin or stdout isn't a terminal, or `TERM` is `dumb`, `zap` prints a plain text summary at the end instead of the live view. `NO_COLOR` is respected.

### `zap history` flags

| Flag | Default | Behaviour |
| --- | --- | --- |
| `--limit <n>` | `20` | Number of most recent runs to show. |
| `--json` | off | Print the runs as a JSON array. |

History is stored as one JSON object per line:

| Platform | Path |
| --- | --- |
| Linux | `$XDG_DATA_HOME/zap/history.jsonl`, or `~/.local/share/zap/history.jsonl` |
| macOS | `~/Library/Application Support/zap/history.jsonl` |
| Windows | `%LocalAppData%\zap\history.jsonl` |

### Exit codes

| Code | Meaning |
| --- | --- |
| `0` | The command completed. |
| `1` | The command failed, for example a network error or Cloudflare rejecting requests. |
| `2` | Invalid usage. |
| `130` | Cancelled by the user. |
| `143` | Terminated by SIGTERM. |

## Reading the results

- **Speeds** are in decimal megabits per second, where 1 Mbps = 1,000,000 bits per second.
- **The headline speed leaves out ramp-up**, the first 20% of each phase. It therefore can't be derived from `bytes` and the duration in the JSON output.
- **Loaded latency** is the latency measured while a transfer is running. A figure shown as `>=` is a lower bound, because more than half its probes were still waiting at the end of the phase. `n/a` means no probe succeeded, and JSON has `null`.
- **Data used** is the payload transferred in both phases. It leaves out latency probes and TCP/TLS overhead, so it is a slight undercount of the data a run really uses. A default run on a fast link can use several gigabytes.

## Behind a proxy

`zap` honours the standard proxy environment variables, and warns when one is in use, because results then measure the path through the proxy.

## Notes

`zap` uses Cloudflare's speed test endpoints, which aren't a supported public API and may change or be rate limited. If Cloudflare rejects a request, the run ends with a clear error and nothing is saved. This project isn't affiliated with Cloudflare.

## Licence

MIT
