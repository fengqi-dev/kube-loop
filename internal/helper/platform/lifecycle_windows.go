//go:build windows

package platform

import (
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/fengqi-dev/kube-loop/internal/singbox"
)

const windowsSearchBackup = "search-domains.bak.json"

func ApplyLinkDNS(string, singbox.DNSMeta) error { return nil }
func RestoreLinkDNS(string) error                { return nil }

func ApplyDNS(workDir string, dns singbox.DNSMeta) error {
	var b strings.Builder
	b.WriteString("$ErrorActionPreference='Stop'; ")
	b.WriteString("$backup = @((Get-DnsClientGlobalSetting).SuffixSearchList) | ConvertTo-Json -Compress; ")
	b.WriteString("Set-Content -LiteralPath " + powershellLiteral(filepath.Join(workDir, windowsSearchBackup)) + " -Value $backup -Encoding utf8; ")
	b.WriteString(`Get-DnsClientNrptRule -ErrorAction SilentlyContinue | Where-Object { $_.Comment -eq 'KubeLoop' } | ForEach-Object { Remove-DnsClientNrptRule -Name $_.Name -Force -ErrorAction SilentlyContinue }; `)
	for _, domain := range dns.Domains {
		fmt.Fprintf(&b,
			"Add-DnsClientNrptRule -Namespace %s -NameServers %s -Comment 'KubeLoop'; ",
			powershellLiteral("."+strings.TrimPrefix(domain, ".")),
			powershellLiteral(dns.Listen),
		)
	}
	if len(dns.Search) > 0 {
		items := make([]string, 0, len(dns.Search))
		for _, domain := range dns.Search {
			items = append(items, powershellLiteral(domain))
		}
		fmt.Fprintf(&b,
			"$want=@(%s); $old=@((Get-DnsClientGlobalSetting).SuffixSearchList); "+
				"$merged=@($want+($old|Where-Object { $_ -and ($want -notcontains $_) })); "+
				"Set-DnsClientGlobalSetting -SuffixSearchList $merged; ",
			strings.Join(items, ","),
		)
	}
	b.WriteString("Clear-DnsClientCache")
	if _, err := runPowerShell(b.String()); err != nil {
		return fmt.Errorf("configure Windows DNS: %w", err)
	}
	return nil
}

func RestoreDNS(workDir string, _ singbox.DNSMeta) error {
	var b strings.Builder
	b.WriteString("$ErrorActionPreference='Continue'; ")
	b.WriteString(`Get-DnsClientNrptRule -ErrorAction SilentlyContinue | Where-Object { $_.Comment -eq 'KubeLoop' } | ForEach-Object { Remove-DnsClientNrptRule -Name $_.Name -Force -ErrorAction SilentlyContinue }; `)
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
	if _, err := runPowerShell(b.String()); err != nil {
		return err
	}
	return nil
}

func runPowerShell(command string) ([]byte, error) {
	output, err := exec.Command(
		"powershell.exe", "-NoProfile", "-NonInteractive",
		"-ExecutionPolicy", "Bypass", "-Command", command,
	).CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(output))
		if detail == "" {
			return output, err
		}
		return output, fmt.Errorf("%w: %s", err, detail)
	}
	return output, nil
}

func powershellLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func CleanupRoutes(routes []string) {
	for _, raw := range routes {
		prefix, err := netip.ParsePrefix(raw)
		if err == nil {
			_ = exec.Command("route.exe", "delete", prefix.Masked().String()).Run()
		}
	}
}
