{{- define "anvil.operatorName" -}}
{{- printf "%s-anvil-operator" .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- define "anvil.operatorImage" -}}
{{- $digest := required "operatorImage.digest must contain the published SHA-256 digest" .Values.operatorImage.digest -}}
{{- if not (regexMatch "^sha256:[a-f0-9]{64}$" $digest) -}}
{{- fail "operatorImage.digest must be a SHA-256 digest" -}}
{{- end -}}
{{- printf "%s@%s" .Values.operatorImage.repository $digest -}}
{{- end -}}
