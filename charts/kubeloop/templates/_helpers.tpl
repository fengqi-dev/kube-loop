{{- define "kubeloop.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "kubeloop.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name (include "kubeloop.name" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}

{{- define "kubeloop.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | quote }}
app.kubernetes.io/name: {{ include "kubeloop.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/part-of: kubeloop
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{- define "kubeloop.selectorLabels" -}}
app.kubernetes.io/name: {{ include "kubeloop.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "kubeloop.controlPlaneName" -}}
{{- printf "%s-control-plane" (include "kubeloop.fullname" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "kubeloop.dataPlaneName" -}}
{{- printf "%s-gateway" (include "kubeloop.fullname" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "kubeloop.operatorName" -}}
{{- printf "%s-operator" (include "kubeloop.fullname" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "kubeloop.controlPlaneRegistryName" -}}
{{- printf "%s-relay" (include "kubeloop.controlPlaneName" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "kubeloop.controlPlaneManagementName" -}}
{{- printf "%s-management" (include "kubeloop.controlPlaneName" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "kubeloop.controlPlaneServiceAccountName" -}}
{{- if .Values.controlPlane.serviceAccount.create -}}
{{- default (include "kubeloop.controlPlaneName" .) .Values.controlPlane.serviceAccount.name -}}
{{- else -}}
{{- required "controlPlane.serviceAccount.name is required when create=false" .Values.controlPlane.serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{- define "kubeloop.dataPlaneServiceAccountName" -}}
{{- if .Values.dataPlane.serviceAccount.create -}}
{{- default (include "kubeloop.dataPlaneName" .) .Values.dataPlane.serviceAccount.name -}}
{{- else -}}
{{- required "dataPlane.serviceAccount.name is required when create=false" .Values.dataPlane.serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{- define "kubeloop.operatorServiceAccountName" -}}
{{- if .Values.operator.serviceAccount.create -}}
{{- default (include "kubeloop.operatorName" .) .Values.operator.serviceAccount.name -}}
{{- else -}}
{{- required "operator.serviceAccount.name is required when create=false" .Values.operator.serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{- define "kubeloop.controlPlaneImage" -}}
{{- printf "%s:%s" .Values.controlPlane.image.repository (default .Chart.AppVersion .Values.controlPlane.image.tag) -}}
{{- end -}}

{{- define "kubeloop.dataPlaneImage" -}}
{{- printf "%s:%s" .Values.dataPlane.image.repository (default .Chart.AppVersion .Values.dataPlane.image.tag) -}}
{{- end -}}

{{- define "kubeloop.operatorImage" -}}
{{- printf "%s:%s" .Values.operator.image.repository (default .Chart.AppVersion .Values.operator.image.tag) -}}
{{- end -}}

{{- define "kubeloop.serviceID" -}}
{{- default (include "kubeloop.fullname" .) .Values.serviceID -}}
{{- end -}}

{{- define "kubeloop.sqliteClaimName" -}}
{{- default (printf "%s-data" (include "kubeloop.controlPlaneName" .)) .Values.controlPlane.storage.sqlite.persistence.existingClaim -}}
{{- end -}}

{{- define "kubeloop.controlPlaneConfigName" -}}
{{- printf "%s-config" (include "kubeloop.controlPlaneName" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "kubeloop.dataPlaneConfigName" -}}
{{- printf "%s-config" (include "kubeloop.dataPlaneName" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "kubeloop.validateExternalAccess" -}}
{{- $publicURL := trimSuffix "/" (trim .Values.publicURL) -}}
{{- $managementPublicURL := trimSuffix "/" (trim .Values.controlPlane.management.publicURL) -}}
{{- if not (regexMatch `^(https://[^/?#]+|http://(localhost|127\.0\.0\.1|\[::1\])(:[0-9]+)?)$` $publicURL) -}}
{{- fail "publicURL must be one HTTPS origin without a path, query or fragment (HTTP is allowed only for loopback development)" -}}
{{- end -}}
{{- if and .Values.ingress.enabled .Values.gatewayAPI.enabled -}}
{{- fail "ingress.enabled and gatewayAPI.enabled are mutually exclusive" -}}
{{- end -}}
{{- if .Values.ingress.enabled -}}
{{- if ne $managementPublicURL $publicURL -}}
{{- fail "controlPlane.management.publicURL must equal publicURL when ingress is enabled" -}}
{{- end -}}
{{- $host := required "ingress.host is required when ingress.enabled=true" .Values.ingress.host -}}
{{- if ne $publicURL (printf "https://%s" $host) -}}
{{- fail "publicURL must exactly equal https://<ingress.host> when ingress is enabled" -}}
{{- end -}}
{{- if not .Values.ingress.tls.enabled -}}
{{- fail "ingress.tls.enabled must be true because the public endpoint requires TLS" -}}
{{- end -}}
{{- end -}}
{{- if .Values.gatewayAPI.enabled -}}
{{- if ne $managementPublicURL $publicURL -}}
{{- fail "controlPlane.management.publicURL must equal publicURL when Gateway API is enabled" -}}
{{- end -}}
{{- $host := required "gatewayAPI.host is required when gatewayAPI.enabled=true" .Values.gatewayAPI.host -}}
{{- if ne $publicURL (printf "https://%s" $host) -}}
{{- fail "publicURL must exactly equal https://<gatewayAPI.host> when Gateway API is enabled" -}}
{{- end -}}
{{- if .Values.gatewayAPI.gateway.create -}}
{{- $_ := required "gatewayAPI.gateway.className is required when gatewayAPI.gateway.create=true" .Values.gatewayAPI.gateway.className -}}
{{- $_ := required "gatewayAPI.gateway.tls.secretName is required when gatewayAPI.gateway.create=true" .Values.gatewayAPI.gateway.tls.secretName -}}
{{- else -}}
{{- $_ := required "gatewayAPI.parentRef.name is required for an existing Gateway" .Values.gatewayAPI.parentRef.name -}}
{{- $_ := required "gatewayAPI.parentRef.sectionName is required and must identify an HTTPS listener" .Values.gatewayAPI.parentRef.sectionName -}}
{{- end -}}
{{- $_ := required "gatewayAPI.timeouts.controlPlane is required" .Values.gatewayAPI.timeouts.controlPlane -}}
{{- if ne (toString .Values.gatewayAPI.timeouts.tunnel) "0s" -}}
{{- fail "gatewayAPI.timeouts.tunnel must be 0s so the Gateway does not terminate long-lived WebSocket sessions" -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{- define "kubeloop.initialAdminSecretName" -}}
{{- default (printf "%s-initial-admin" (include "kubeloop.controlPlaneName" .)) .Values.controlPlane.management.initialAdmin.existingSecret | trunc 63 | trimSuffix "-" -}}
{{- end -}}
