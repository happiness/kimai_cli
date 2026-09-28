# kimai_cli

A small command line tool for checking your reported time in [Kimai](https://www.kimai.org/).

## Getting started

You do not need Go or Linux to use kimai_cli. Download a ready-made program, create a small
config file and set your API token. Each step is described below for Windows and macOS.

### 1. Download the program

Open the [latest release](https://github.com/happiness/kimai_cli/releases/latest) and download the
file for your computer:

| Computer | File |
|---|---|
| Windows | `kimai_cli-windows-amd64.exe` |
| Mac with Apple Silicon (M1, M2, …) | `kimai_cli-darwin-arm64` |
| Mac with Intel processor | `kimai_cli-darwin-amd64` |
| Linux | `kimai_cli-linux-amd64` (or `-arm64`) |

Not sure which Mac you have? Open the Apple menu → **About This Mac**. If it says **Chip: Apple M…**,
use `darwin-arm64`. If it says **Processor: Intel**, use `darwin-amd64`.

### 2. Get your API token

Create an API token in your user profile in Kimai, under API access. Keep it somewhere safe; you
need it in step 4.

### 3. Create the config file

The config file tells kimai_cli where Kimai is. It only contains the URL, never your token.

**Windows** – open PowerShell and run:

```powershell
mkdir "$env:APPDATA\kimai_cli"
Set-Content "$env:APPDATA\kimai_cli\config.toml" 'url = "https://kimai.example.com/api/"'
```

**macOS** – open Terminal and run:

```sh
mkdir -p ~/Library/"Application Support"/kimai_cli
echo 'url = "https://kimai.example.com/api/"' > ~/Library/"Application Support"/kimai_cli/config.toml
```

**Linux:**

```sh
mkdir -p ~/.config/kimai_cli
echo 'url = "https://kimai.example.com/api/"' > ~/.config/kimai_cli/config.toml
```

### 4. Set your API token

The token is only read from the `KIMAI_TOKEN` environment variable, so it is never written to the
config file.

**Windows** – in PowerShell:

```powershell
setx KIMAI_TOKEN "your-api-token"
```

**macOS** – in Terminal:

```sh
echo 'export KIMAI_TOKEN="your-api-token"' >> ~/.zshrc
```

**Linux:** add `export KIMAI_TOKEN="your-api-token"` to your `~/.bashrc` (or your shell's equivalent).

Close the window and open a new PowerShell or Terminal window afterwards, so the token is picked up.

### 5. Run it

**Windows** – in PowerShell, go to the folder where you downloaded the file and run it:

```powershell
cd ~\Downloads
Rename-Item kimai_cli-windows-amd64.exe kimai_cli.exe
.\kimai_cli.exe
```

The first time, Windows SmartScreen may warn that the app is unrecognised. Click **More info** →
**Run anyway**.

**macOS** – in Terminal (use `darwin-amd64` in the first line on an Intel Mac):

```sh
cd ~/Downloads
mv kimai_cli-darwin-arm64 kimai_cli
chmod +x kimai_cli
xattr -d com.apple.quarantine kimai_cli
./kimai_cli
```

The `xattr` line is needed because the program is not signed by Apple; without it, macOS refuses
to open it.

You should see a line like `t: 6.5 (of 8), w: 22.0 (of 24/40)`. See [Usage](#usage) for the other
commands. On Windows, write `.\kimai_cli.exe` wherever the examples say `kimai_cli`.

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

Building from source requires Go 1.27 or later.

```sh
go build -o kimai_cli .
go install .   # or install it into $GOPATH/bin
```

Run the tests and checks with:

```sh
go test ./...
go vet ./...
```

The tests use a local `httptest` server and make no calls to a real Kimai instance.

### Releases

Pushing a version tag builds binaries for Linux, macOS and Windows and attaches them to a
GitHub Release (see `.github/workflows/release.yml`):

```sh
git tag v1.0.0
git push origin v1.0.0
```

## Future goal

My goal with this small tool is to get away from the Kimai web interface as much as possible, as going into the web interface occasionally interrupts my workflow. That means the next step will be allowing time reporting to be done directly from the command line. There is also a case for adding filtering by a specific date.

## Why is it written in Go

While most projects at Happiness are written in PHP, which is the main language we work in for web development, this started as a personal tool, and while PHP could do it, I had missed writing something in Go for a while. Because Go is a compiled language, it will also be quicker than PHP as a scripting language. In practice the difference is small, though: a response time of 0.692 s versus 1.296 s is barely noticeable, and much of that time is spent waiting for the Kimai API anyway.
