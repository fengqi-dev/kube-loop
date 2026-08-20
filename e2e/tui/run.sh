#!/bin/sh
set -eu

if ! command -v expect >/dev/null 2>&1; then
	echo "TUI E2E requires expect" >&2
	exit 1
fi

repository_root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
artifact_dir=$(mktemp -d "${TMPDIR:-/tmp}/kubeloop-tui-e2e.XXXXXX")
test_binary="$artifact_dir/tui.test"

cd "$repository_root"
go test -c -o "$test_binary" ./internal/tui

export KUBELOOP_TUI_E2E_BINARY="$test_binary"
export KUBELOOP_TUI_E2E_HOME="$artifact_dir/home"
mkdir -p "$KUBELOOP_TUI_E2E_HOME"

expect <<'EXPECT'
set timeout 10
set binary $env(KUBELOOP_TUI_E2E_BINARY)
set home $env(KUBELOOP_TUI_E2E_HOME)

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
	after 100
	foreach character [split $value ""] {
		send -- $character
		after 20
	}
}

proc click {column row} {
	send -- "\033\[<0;${column};${row}M"
}

proc handshake {description pattern} {
	expect {
		-re {\x1b\]11;\?\x1b\\} {
			send -- "\033]11;rgb:0000/0000/0000\033\\"
			exp_continue
		}
		-re {\x1b\[6n} {
			send -- "\033\[1;1R"
			exp_continue
		}
		-re $pattern { puts "PASS: $description" }
		timeout { fail $description }
		eof { fail "$description (process exited early)" }
	}
}

spawn env HOME=$home TERM=xterm-256color KUBELOOP_TUI_E2E_FIXTURE=1 $binary -test.v -test.run {^TestTUIFixture$}
exec stty rows 32 columns 120 < $spawn_out(slave,name)
exec kill -WINCH [exp_pid]
handshake "launch k9s workspace" {KUBELOOP}
expect_text "global status header" {Cluster:.*KubeLoop Rev:.*K8s Rev:}
expect_text "connection resource" {<connection>}

send -- "n"
expect_text "namespace resource" {Namespaces\[3\]}
send -- "/"
type_text "prod"
expect_text "namespace regex filter" {Namespaces\[1\].*Filter: /prod}
send -- "\r"
after 100
send -- "\r"
expect_text "namespace selection opens pods" {Pods\(production\)\[1\]}

send -- "\r"
expect_text "pod detail" {Pods / api-0}
send -- "\033"
expect_text "pod list restored" {Pods\(production\)\[1\]}
send -- "f"
expect_text "pod forward action" {PORT FORWARD}
click 91 20
expect_text "pod forward mouse cancel" {Pods\(production\)\[1\]}
send -- ":"
expect_text "command candidates" {Tab complete}
type_text "services"
send -- "\r"
expect_text "services command" {Services\(production\)\[1\]}
send -- "/"
type_text "zzz"
expect_text "filter empty state" {No matches for /zzz}
send -- "\033"
expect_text "filter cancel restores rows" {Services\(production\)\[1\]}
send -- "/"
type_text "-f ap"
expect_text "fuzzy filter" {Services\(production\)\[1\].*Filter: /-fap}
send -- "\r"
send -- "\033"
expect_text "applied filter clear" {Services\(production\)\[1\]}

send -- ":"
type_text "sessions"
send -- "\r"
expect_text "sessions command" {Sessions\[2\]}
expect_text "session output row" {EXEC.*api-0.*env}
send -- "C"
expect_text "clear completed sessions" {Cleared 1 completed exec session}
send -- "\r"
expect_text "session detail" {Sessions / api-0}
send -- "\033"
after 100
send -- "e"
expect_text "rerun exec" {EXECUTE COMMAND}
click 91 20
send -- "y"
expect_text "copy output" {Session output copied}
send -- "d"
expect_text "session stop confirmation" {STOP SESSION\?}
click 91 20
expect_text "session stop mouse cancel" {Sessions\[1\]}
send -- "d"
after 100
send -- "y"
expect_text "session stopped" {Session stopped}

send -- "\["
expect_text "resource history back" {Services\(production\)}
send -- "\]"
expect_text "resource history forward" {Sessions\[1\]}
send -- "?"
expect_text "workspace help" {K9S WORKSPACE HELP}
send -- "?"
after 100

send -- ":"
type_text "servers"
send -- "\r"
expect_text "server resource" {Servers\[[0-9]+\]}
send -- ":"
type_text "connection"
send -- "\r"
expect_text "connection command" {<connection>}
send -- "m"
expect_text "SOCKS mode" {MODE SOCKS}
send -- "\r"
expect_text "fixture connect" {CONNECTED}
send -- "\r"
expect_text "disconnect confirmation" {DISCONNECT\?}
send -- "y"
expect_text "fixture disconnect" {Data plane disconnected}

exec stty rows 16 columns 55 < $spawn_out(slave,name)
exec kill -WINCH [exp_pid]
expect_text "minimum size guard" {Current: 55x16  Required: 60x18}
exec stty rows 24 columns 90 < $spawn_out(slave,name)
exec kill -WINCH [exp_pid]
expect_text "resize recovery" {Connection}

send -- ":"
type_text "q"
send -- "\r"
expect {
	-re {--- PASS: TestTUIFixture} { puts "PASS: main fixture clean exit"; exp_continue }
	eof {}
	timeout { fail "main fixture clean exit" }
}

spawn env HOME=$home TERM=xterm-256color KUBELOOP_TUI_E2E_FIXTURE=1 $binary -test.v -test.run {^TestTUIProfileFixture$}
exec stty rows 28 columns 100 < $spawn_out(slave,name)
exec kill -WINCH [exp_pid]
handshake "launch server resource" {Servers\[2\]}
expect_text "server rows" {Primary.*primary.example.test}
send -- "a"
expect_text "server add form" {ADD SERVER}
send -- "\033\[200~https://console.example.test/path\033\[201~"
expect_text "profile URL paste" {console.example.test/path}
send -- "\t"
type_text "UITestProfile"
expect_text "profile name input" {UITestProfile}
click 76 18
expect_text "server add mouse cancel" {Servers\[2\]}
send -- "d"
expect_text "server delete confirmation" {DELETE SERVER\?}
click 76 17
expect_text "server delete mouse cancel" {Servers\[2\]}
send -- "?"
expect_text "server help" {K9S WORKSPACE HELP}
send -- "?"
send -- "\003"
expect {
	-re {--- PASS: TestTUIProfileFixture} { puts "PASS: profile fixture clean exit"; exp_continue }
	eof {}
	timeout { fail "profile fixture clean exit" }
}
EXPECT

echo "TUI E2E passed"
echo "Artifacts: $artifact_dir"
