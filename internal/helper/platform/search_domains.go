package platform

import "strings"

func kubernetesSearchRoots(domains []string) []string {
	seen := make(map[string]struct{})
	var roots []string
	for _, domain := range domains {
		domain = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(domain)), ".")
		root := ""
		if value, ok := strings.CutPrefix(domain, "svc."); ok {
			root = value
		} else if _, value, ok := strings.Cut(domain, ".svc."); ok {
			root = value
		}
		if root == "" {
			continue
		}
		if _, ok := seen[root]; ok {
			continue
		}
		seen[root] = struct{}{}
		roots = append(roots, root)
	}
	return roots
}

func windowsSearchDomainMergeScript(desired []string) string {
	if len(desired) == 0 {
		return ""
	}
	want := make([]string, 0, len(desired))
	for _, domain := range desired {
		want = append(want, powershellLiteral(domain))
	}
	roots := kubernetesSearchRoots(desired)
	rootLiterals := make([]string, 0, len(roots))
	for _, root := range roots {
		rootLiterals = append(rootLiterals, powershellLiteral(root))
	}

	var b strings.Builder
	b.WriteString("$want=@(" + strings.Join(want, ",") + "); ")
	b.WriteString("$roots=@(" + strings.Join(rootLiterals, ",") + "); ")
	b.WriteString("$old=@((Get-DnsClientGlobalSetting).SuffixSearchList); $preserved=@(); ")
	b.WriteString("foreach ($item in $old) { if (-not $item) { continue }; ")
	b.WriteString("$value=([string]$item).Trim().TrimEnd('.').ToLowerInvariant(); $isKubernetes=$false; ")
	b.WriteString("foreach ($root in $roots) { if ($value -eq $root -or $value -eq ('svc.'+$root) -or $value.EndsWith('.svc.'+$root)) { $isKubernetes=$true; break } }; ")
	b.WriteString("if (-not $isKubernetes -and ($want -notcontains $item)) { $preserved += $item } }; ")
	b.WriteString("$merged=@($want+$preserved); Set-DnsClientGlobalSetting -SuffixSearchList $merged; ")
	return b.String()
}

func powershellLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}
