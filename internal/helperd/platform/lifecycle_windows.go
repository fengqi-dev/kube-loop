//go:build windows

package platform

import (
	"context"
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/protocol/sessionspec"
)

const windowsSearchBackup = "search-domains.bak.json"

func ApplyLinkDNS(string, sessionspec.DNSMeta) error { return nil }
func RestoreLinkDNS(string) error                    { return nil }

func ApplyDNS(workDir string, dns sessionspec.DNSMeta) error {
	var b strings.Builder
	b.WriteString("$ErrorActionPreference='Stop'; ")
	b.WriteString("$backup = @((Get-DnsClientGlobalSetting).SuffixSearchList) | ConvertTo-Json -Compress; ")
	b.WriteString(
		"Set-Content -LiteralPath " + powershellLiteral(
			filepath.Join(workDir, windowsSearchBackup),
		) + " -Value $backup -Encoding utf8; ",
	)
	b.WriteString(
		`Get-DnsClientNrptRule -ErrorAction SilentlyContinue | Where-Object { $_.Comment -eq 'KubeLoop' } | ForEach-Object { Remove-DnsClientNrptRule -Name $_.Name -Force -ErrorAction SilentlyContinue }; `,
	)
	for _, domain := range dns.Domains {
		fmt.Fprintf(&b,
			"Add-DnsClientNrptRule -Namespace %s -NameServers %s -Comment 'KubeLoop'; ",
			powershellLiteral("."+strings.TrimPrefix(domain, ".")),
			powershellLiteral(dns.Listen),
		)
	}
	b.WriteString(windowsSearchDomainMergeScript(dns.Search))
	b.WriteString("Clear-DnsClientCache")
	if err := runPowerShell(b.String()); err != nil {
		return fmt.Errorf("configure Windows DNS: %w", err)
	}
	return nil
}

func RestoreDNS(workDir string, _ sessionspec.DNSMeta) error {
	var b strings.Builder
	b.WriteString("$ErrorActionPreference='Continue'; ")
	b.WriteString(
		`Get-DnsClientNrptRule -ErrorAction SilentlyContinue | Where-Object { $_.Comment -eq 'KubeLoop' } | ForEach-Object { Remove-DnsClientNrptRule -Name $_.Name -Force -ErrorAction SilentlyContinue }; `,
	)
	backupPath := filepath.Join(workDir, windowsSearchBackup)
	backup, readErr := os.ReadFile(backupPath)
	if readErr == nil {
		raw := strings.TrimSpace(string(backup))
		// PowerShell Set-Content may write a UTF-8 BOM; strip it for JSON restore.
		raw = strings.TrimPrefix(raw, "\ufeff")
		if raw == "" || raw == "null" || raw == "[]" {
			b.WriteString("Set-DnsClientGlobalSetting -SuffixSearchList @(); ")
		} else {
			fmt.Fprintf(&b,
				"$old=%s | ConvertFrom-Json; if ($old -isnot [array]) { $old=@($old) }; "+
					"Set-DnsClientGlobalSetting -SuffixSearchList $old; ",
				powershellLiteral(raw),
			)
		}
		_ = os.Remove(backupPath)
	}
	b.WriteString("Clear-DnsClientCache")
	if err := runPowerShell(b.String()); err != nil {
		return err
	}
	return nil
}

func runPowerShell(command string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	output, err := exec.CommandContext(
		ctx,
		"powershell.exe", "-NoProfile", "-NonInteractive",
		"-ExecutionPolicy", "Bypass", "-Command", command,
	).CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(output))
		if detail == "" {
			return err
		}
		return fmt.Errorf("%w: %s", err, detail)
	}
	return nil
}

func CleanupRoutes(routes []string) {
	for _, raw := range routes {
		prefix, err := netip.ParsePrefix(raw)
		if err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			// The route argument is canonical output from netip.ParsePrefix, not raw input.
			_ = exec.CommandContext( //nolint:gosec // Canonical network prefix cannot inject command arguments.
				ctx, "route.exe", "delete", prefix.Masked().String(),
			).Run()
			cancel()
		}
	}
}
