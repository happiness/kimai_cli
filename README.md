# kimai_cli

A small command line tool for checking your reported time in [Kimai](https://www.kimai.org/).

## Install

Requires Go 1.27 or later.

```sh
go build -o kimai_cli .
```

Or install it into `$GOPATH/bin`:

```sh
go install .
```

## Configuration

### Kimai URL

The URL of your Kimai API is read from a config file, which is required:

```toml
# ~/.config/kimai_cli/config.toml
url = "https://kimai.example.com/api/"
```

The file lives in your user config directory (`$XDG_CONFIG_HOME` or `~/.config` on Linux,
`~/Library/Application Support` on macOS, `%AppData%` on Windows).

```sh
mkdir -p ~/.config/kimai_cli
echo 'url = "https://kimai.example.com/api/"' > ~/.config/kimai_cli/config.toml
```

### API token

The token is only read from the `KIMAI_TOKEN` environment variable, so it is never written to a file.
Create an API token in your Kimai user profile and export it:

```sh
export KIMAI_TOKEN="your-api-token"
```

## Usage

```sh
kimai_cli [command]
```

| Command | Description |
|---|---|
| `short` | One-line summary of today and the current week. Default when no command is given. |
| `today` | All timesheets reported today. |
| `week` | All timesheets for Monday–Friday of the current week, with a total per day and for the week. |
| `search` (or `s`) | Timesheets for a customer that match a description. |

### short

```sh
$ kimai_cli
t: 6.5 (of 8), w: 22.0 (of 24/40)
```

`t` is the hours reported today. `w` is the hours reported this week, compared with the target
so far (8 hours per weekday, up to 40).

### today / week

Each timesheet is printed as:

```
<customer> <project> <activity id> - <description> <hours>
```

### search

Both flags are required:

```sh
kimai_cli search -customer acme -description "sprint planning"
```

This prints every timesheet whose description matches, belonging to a customer whose name matches,
followed by the total hours.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | Success |
| 1 | Error, for example a missing config file or a failed API request |
| 2 | Unknown command |
| 3 | `KIMAI_TOKEN` is not set |

## Development

```sh
go test ./...
go vet ./...
```

The tests use a local `httptest` server and make no calls to a real Kimai instance.

## Future goal

My goal with this small tool is to get away from the Kimai web interface as much as possible, as going into the web interface occasionally interrupts my workflow. That means the next step will be allowing time reporting to be done directly from the command line. There is also a case for adding filtering by a specific date.

## Why is it written in Go

While most projects at Happiness are written in PHP, which is the main language we work in for web development, this started as a personal tool, and while PHP could do it, I had missed writing something in Go for a while. Because Go is a compiled language, it will also be quicker than PHP as a scripting language. In practice the difference is small, though: a response time of 0.692 s versus 1.296 s is barely noticeable, and much of that time is spent waiting for the Kimai API anyway.
