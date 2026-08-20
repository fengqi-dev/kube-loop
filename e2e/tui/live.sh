#!/bin/sh
set -eu

if [ "${1:-}" = "--check" ]; then
	command -v expect >/dev/null 2>&1 || {
		echo "TUI live E2E requires expect" >&2
		exit 1
	}
	echo "TUI live E2E contract is available"
	exit 0
fi

if ! command -v expect >/dev/null 2>&1; then
	echo "TUI live E2E requires expect" >&2
	exit 1
fi

: "${KUBELOOP_TUI_E2E_LIVE_HOME:?Set KUBELOOP_TUI_E2E_LIVE_HOME to an isolated, pre-authenticated TUI home directory}"

if [ ! -d "$KUBELOOP_TUI_E2E_LIVE_HOME" ]; then
	echo "KUBELOOP_TUI_E2E_LIVE_HOME does not exist: $KUBELOOP_TUI_E2E_LIVE_HOME" >&2
	exit 1
fi

repository_root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
artifact_dir=$(mktemp -d "${TMPDIR:-/tmp}/kubeloop-tui-live-e2e.XXXXXX")
binary="$artifact_dir/kubeloop-tui"

cd "$repository_root"
make tui TUI_BINARY="$binary"

export KUBELOOP_TUI_E2E_LIVE_BINARY="$binary"
export KUBELOOP_TUI_E2E_LIVE_CONNECT="${KUBELOOP_TUI_E2E_LIVE_CONNECT:-0}"

expect <<'EXPECT'
set timeout 20
set binary $env(KUBELOOP_TUI_E2E_LIVE_BINARY)
set home $env(KUBELOOP_TUI_E2E_LIVE_HOME)
set connect $env(KUBELOOP_TUI_E2E_LIVE_CONNECT)

proc fail {message} {
	puts stderr "FAIL: $message"
	exit 1
}

proc expect_text {description pattern} {
	expect {
		-re $pattern { puts "PASS: $description" }
		timeout { fail "$description (timeout waiting for $pattern)" }
		eof { fail "$description (process exited early)" }
	}
}

proc type_text {value} {
	foreach character [split $value ""] {
		send -- $character
		after 35
	}
}

proc run_command {value} {
	send -- ":"
	after 80
	type_text $value
	send -- "\r"
}

spawn env HOME=$home TERM=xterm-256color $binary
exec stty rows 32 columns 120 < $spawn_out(slave,name)
exec kill -WINCH [exp_pid]
expect {
	-re {\x1b\]11;\?\x1b\\} {
		send -- "\033]11;rgb:0000/0000/0000\033\\"
		exp_continue
	}
	-re {\x1b\[6n} {
		send -- "\033\[1;1R"
		exp_continue
	}
	-re {<connection>} { puts "PASS: authenticated live TUI launch" }
	-re {Profiles\[[0-9]+\]} { fail "live HOME is not authenticated" }
	timeout { fail "authenticated live TUI launch" }
	eof { fail "authenticated live TUI launch (process exited early)" }
}

send -- "?"
expect_text "live help overlay" {K9S WORKSPACE HELP}
send -- "?"
after 200
run_command "pods"
expect_text "live pods navigation" {Pods\(}
run_command "services"
expect_text "live services navigation" {Services\(}
run_command "sessions"
expect_text "live sessions navigation" {Sessions\[}
run_command "connection"
expect_text "live connection navigation" {<connection>}

if {$connect == "1"} {
	set timeout 60
	send -- "\r"
	expect_text "live SOCKS connection" {Data plane connected}
	run_command "connection"
	expect_text "live connection after connect" {<connection>}
	send -- "m"
	expect_text "live TUN switch" {Mode switched to tun}
	send -- "m"
	expect_text "live SOCKS switch" {Mode switched to socks}
	send -- "\r"
	expect {
		-re {Data plane disconnected} { puts "PASS: live disconnect" }
		-re {DISCONNECT\?} {
			send -- "y"
			expect_text "live confirmed disconnect" {Data plane disconnected}
		}
		timeout { fail "live disconnect" }
		eof { fail "live disconnect (process exited early)" }
	}
}

send -- "\003"
expect eof
EXPECT

echo "TUI live E2E passed"
echo "Artifacts: $artifact_dir"
