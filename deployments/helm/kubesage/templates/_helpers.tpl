{{- define "kubesage.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "kubesage.fullname" -}}
{{- printf "%s-%s" .Release.Name (include "kubesage.name" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "kubesage.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "kubesage.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}
