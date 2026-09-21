# Omabudget FAQ

## Why does omabudget build from source?

The plugin ships Go source, not binaries. `bin/` is not committed, so the
daemon and the command line tool are compiled on the machine that runs them.
Building needs the Go toolchain and git.

## The build says the Go toolchain is not on PATH

The preflight tells you which of three situations you are in. This page has
the commands for each.

### mise has Go, but this shell cannot see it

mise activates per shell, so a shell opened before you installed Go does not
have it. Open a new terminal and build again, or point the build straight at
the toolchain mise already has:

```
make GO=$(mise which go)
```

### mise is installed, but has no Go

Omarchy ships mise, and it needs no root:

```
mise use -g go@latest
```

Then open a new terminal and build again.

### No mise, no Go

Install Go with the system package manager:

```
sudo pacman -S go
```

Or install mise first and use the previous answer, which keeps toolchains in
your home directory instead of system wide.

## Which Go version?

The minimum is the version in `go.mod`. The preflight prints it.

The build sets `GOTOOLCHAIN=local`, so it uses the toolchain you installed
rather than downloading a different one over the network. If your Go is older
than `go.mod` asks for, the build says so instead of silently fetching another.

## Does the SQLite database need a C compiler?

No. The driver is `modernc.org/sqlite`, which is pure Go, so the build sets
`CGO_ENABLED=0` and needs no C toolchain at all. That also keeps the binaries
reproducible: cgo turns itself on whenever it finds a C compiler, which would
otherwise make the same source produce different bytes on different machines.

The race detector does need cgo, so the test target turns it back on for
itself alone.

## Where does my data live?

`~/.config/omabudget/`, holding `config.json`, `ledger.db` and
`triggers.json`, all mode 0600. Nothing leaves the machine. Rebuilding or
reinstalling the plugin does not touch any of it.

The ledger is deliberately not kept in a synced folder, because a database
synced while open corrupts. Use `omabudget export` for a plain text journal or
`omabudget backup` for a consistent copy.

## The panel says omabudget is not built yet

The QML is installed but `bin/` is empty. Run `make` in the plugin directory,
then restart the shell.
