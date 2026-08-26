{{/*
Chart name, truncated/sanitized per Helm's own convention.
*/}}
{{- define "axiaops.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Fully-qualified app name. One release = one environment (mirrors one
self-hosted host = one environment today), so releaseName alone is normally enough --
this still guards the multi-release-per-namespace edge case the same way
every Helm starter chart does.
*/}}
{{- define "axiaops.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- $name := default .Chart.Name .Values.nameOverride -}}
{{- if contains $name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{- define "axiaops.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "axiaops.labels" -}}
helm.sh/chart: {{ include "axiaops.chart" . }}
{{ include "axiaops.selectorLabels" . }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{/*
Component-scoped selector labels. component is one of: api, ingestion,
dashboard, valkey, migrate.
*/}}
{{- define "axiaops.selectorLabels" -}}
app.kubernetes.io/name: {{ include "axiaops.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "axiaops.componentLabels" -}}
{{ include "axiaops.labels" .context }}
app.kubernetes.io/component: {{ .component }}
{{- end -}}

{{- define "axiaops.componentSelectorLabels" -}}
{{ include "axiaops.selectorLabels" .context }}
app.kubernetes.io/component: {{ .component }}
{{- end -}}

{{/*
Resolves the image tag. Deliberately NOT defaulted to .Chart.AppVersion --
CI tags images main-<shortsha>, not semver, so a silent fallback to
Chart.AppVersion would resolve to a tag that's never actually been
published and fail with ErrImagePull. image.tag must be set explicitly;
fail loudly at render time instead of at pod-crash time.
*/}}
{{- define "axiaops.imageTag" -}}
{{- required "image.tag must be set -- see the chart README for how to find a published tag" .Values.image.tag -}}
{{- end -}}

{{/*
api/ingestion share one tag-suffix knob: empty (the default) pulls the
DEV_MODE-hardwired-off production image; "-devmode" pulls the sibling build
that honours a DEV_MODE env var at runtime. Safe image is the default on
purpose -- see values.yaml's image.apiSuffix comment.
*/}}
{{- define "axiaops.apiImage" -}}
{{- printf "%s/api:%s%s" .Values.image.registry (include "axiaops.imageTag" .) .Values.image.apiSuffix -}}
{{- end -}}

{{- define "axiaops.ingestionImage" -}}
{{- printf "%s/ingestion:%s%s" .Values.image.registry (include "axiaops.imageTag" .) .Values.image.apiSuffix -}}
{{- end -}}

{{- define "axiaops.migrateImage" -}}
{{- printf "%s/migrate:%s" .Values.image.registry (include "axiaops.imageTag" .) -}}
{{- end -}}

{{- define "axiaops.dashboardImage" -}}
{{- printf "%s/dashboard:%s" .Values.image.registry (include "axiaops.imageTag" .) -}}
{{- end -}}

{{/*
redis://[:password@]host:port -- built once here so api/ingestion
don't each re-derive it (matches deploy/dev.yml's REDIS_URL default, which
does the same conditional-userinfo construction per-service today).
*/}}
{{- define "axiaops.redisHost" -}}
{{- printf "%s-valkey" (include "axiaops.fullname" .) -}}
{{- end -}}

{{- define "axiaops.ingestionHost" -}}
{{- printf "%s-ingestion" (include "axiaops.fullname" .) -}}
{{- end -}}

{{- define "axiaops.apiHost" -}}
{{- printf "%s-api" (include "axiaops.fullname" .) -}}
{{- end -}}
