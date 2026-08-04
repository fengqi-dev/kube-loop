package locale

// Strings holds native system-dialog copy. Frontend has its own i18n.
type Strings struct {
	SelectKubeconfig string
	KubeconfigFilter string
}

// T returns the string table for the preferred OS language.
func T() Strings {
	if IsChinese() {
		return zh
	}
	return en
}

var en = Strings{
	SelectKubeconfig: "Select kubeconfig",
	KubeconfigFilter: "Kubeconfig",
}

var zh = Strings{
	SelectKubeconfig: "选择 kubeconfig",
	KubeconfigFilter: "Kubeconfig",
}
