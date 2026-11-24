{{- define "agentuse.selectorLabels" -}}
app.kubernetes.io/name: agentuse
app.kubernetes.io/instance: {{ .Release.Name | quote }}
{{- end -}}

{{- define "agentuse.publicURL" -}}
{{- if .Values.publicURL -}}
{{ .Values.publicURL }}
{{- else if .Values.ingress.enabled -}}
{{ ternary "https" "http" (not (empty .Values.ingress.tlsSecretName)) }}://{{ required "ingress.host is required when Ingress is enabled" .Values.ingress.host }}
{{- else -}}
http://localhost:8080
{{- end -}}
{{- end -}}
