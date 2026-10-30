<template>
  <!-- 令牌隧道的路由规则：通过 Cloudflare API 读写远程配置，保存后运行中的 cloudflared 自动生效 -->
  <el-dialog v-model="visible" :title="t('cloudflared.ingress.title', { name })" width="880px" :fullscreen="isMobile" @closed="reset">
    <div v-loading="loading" class="ing">
      <el-alert v-if="loadError" type="error" :title="loadError" :closable="false" show-icon />
      <template v-else-if="loaded">
        <el-alert v-if="source === 'local'" type="warning" :title="t('cloudflared.ingress.localSource')" :closable="false" show-icon class="ing-gap" />
        <div class="form-help ing-gap">{{ t('cloudflared.ingress.help', { id: tunnelId }) }}</div>

        <div v-for="(r, i) in rules" :key="r.id" class="ing-rule">
          <div class="ing-index mono">{{ i + 1 }}</div>
          <div class="ing-fields">
            <div class="ing-line">
              <el-input v-model="r.hostname" :placeholder="t('cloudflared.ingress.hostnamePh')" class="mono ing-host" />
              <el-input v-model="r.path" :placeholder="t('cloudflared.ingress.pathPh')" class="mono ing-path" />
              <!-- 自动补全框的根节点不带作用域属性，外包一层以便按比例分配宽度 -->
              <div class="ing-svc">
                <el-autocomplete v-model="r.service" :fetch-suggestions="serviceSuggestions" :placeholder="t('cloudflared.ingress.servicePh')" class="mono" />
              </div>
            </div>
            <div class="ing-line ing-opts">
              <el-checkbox :model-value="r.originRequest.noTLSVerify === true" @change="(v: string | number | boolean) => setOpt(r, 'noTLSVerify', v === true ? true : undefined)">
                {{ t('cloudflared.ingress.noTlsVerify') }}
              </el-checkbox>
              <el-input
                :model-value="(r.originRequest.httpHostHeader as string) || ''"
                :placeholder="t('cloudflared.ingress.hostHeaderPh')"
                size="small"
                class="mono ing-hh"
                @update:model-value="(v: string) => setOpt(r, 'httpHostHeader', v.trim() || undefined)"
              />
              <span v-if="extraOpts(r)" class="ing-extra" :title="extraOpts(r)">{{ t('cloudflared.ingress.otherOptions', { keys: extraOpts(r) }) }}</span>
            </div>
          </div>
          <div class="ing-actions">
            <el-button text size="small" :icon="ArrowUp" :disabled="i === 0" :title="t('cloudflared.ingress.moveUp')" @click="move(i, -1)" />
            <el-button text size="small" :icon="ArrowDown" :disabled="i === rules.length - 1" :title="t('cloudflared.ingress.moveDown')" @click="move(i, 1)" />
            <el-button text size="small" type="danger" :icon="Trash2" :title="t('common.delete')" @click="rules.splice(i, 1)" />
          </div>
        </div>
        <el-empty v-if="!rules.length" :description="t('cloudflared.ingress.empty')" :image-size="60" />
        <el-button size="small" :icon="Plus" class="ing-gap" @click="add">{{ t('cloudflared.ingress.add') }}</el-button>

        <el-form :label-position="labelPosition" label-width="120px" class="ing-form">
          <el-form-item :label="t('cloudflared.ingress.catchAll')">
            <el-autocomplete v-model="catchAll" :fetch-suggestions="catchAllSuggestions" placeholder="http_status:404" class="mono" />
            <div class="form-help">{{ t('cloudflared.ingress.catchAllHelp') }}</div>
          </el-form-item>
          <el-form-item :label="t('cloudflared.ingress.dns')">
            <el-switch v-model="syncDns" />
            <div class="form-help">{{ t('cloudflared.ingress.dnsHelp', { target: `${tunnelId}.cfargotunnel.com` }) }}</div>
          </el-form-item>
        </el-form>

        <div v-if="dnsResults.length" class="ing-dns">
          <div v-for="d in dnsResults" :key="d.hostname" :class="['ing-dns-row', `is-${d.status}`]">
            <span class="mono">{{ d.hostname }}</span>
            <span>{{ dnsText(d) }}</span>
          </div>
        </div>
      </template>
    </div>
    <template #footer>
      <el-button @click="visible = false">{{ t('common.close') }}</el-button>
      <el-button v-if="loadError" @click="load">{{ t('cloudflared.ingress.reload') }}</el-button>
      <el-button v-else type="primary" :loading="saving" :disabled="!loaded" @click="save">{{ t('common.save') }}</el-button>
    </template>
  </el-dialog>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { ElMessage } from 'element-plus'
import { ArrowDown, ArrowUp, Plus, Trash2 } from 'lucide-vue-next'
import { api } from '../api'
import { useFormLayout } from '../composables/useIsMobile'

interface Rule {
  hostname?: string
  path?: string
  service: string
  originRequest?: Record<string, unknown>
}
// EditRule 是编辑中的规则，id 只用于列表渲染，保存时去掉
interface EditRule {
  id: number
  hostname: string
  path: string
  service: string
  originRequest: Record<string, unknown>
}
interface DNSResult {
  hostname: string
  status: 'created' | 'unchanged' | 'conflict' | 'failed'
  existing?: string
  error?: string
}
interface IngressView {
  tunnelId: string
  version: number
  source: string
  rules: Rule[]
  catchAll: string
  dns?: DNSResult[]
}

const { t } = useI18n()
const { isMobile, labelPosition } = useFormLayout()
const visible = ref(false)
const loading = ref(false)
const saving = ref(false)
const loaded = ref(false)
const loadError = ref('')
const key = ref('')
const name = ref('')
const tunnelId = ref('')
const version = ref(0)
const source = ref('')
const rules = ref<EditRule[]>([])
const catchAll = ref('http_status:404')
const syncDns = ref(true)
const dnsResults = ref<DNSResult[]>([])
let nextId = 1

// 界面直接编辑的回源设置，其他字段在规则中原样保留
const editedOpts = ['noTLSVerify', 'httpHostHeader']

function toEdit(r: Rule): EditRule {
  return { id: nextId++, hostname: r.hostname || '', path: r.path || '', service: r.service, originRequest: { ...(r.originRequest || {}) } }
}

function apply(v: IngressView) {
  tunnelId.value = v.tunnelId
  version.value = v.version
  source.value = v.source
  rules.value = v.rules.map(toEdit)
  catchAll.value = v.catchAll
}

function reset() {
  loaded.value = false
  loadError.value = ''
  dnsResults.value = []
  rules.value = []
}

async function load() {
  loading.value = true
  loadError.value = ''
  try {
    apply(await api.get<IngressView>(`/api/cloudflared/tunnels/${key.value}/ingress`))
    loaded.value = true
  } catch (e) {
    loadError.value = (e as Error).message
  } finally {
    loading.value = false
  }
}

function open(tunnelKey: string, tunnelName: string) {
  reset()
  key.value = tunnelKey
  name.value = tunnelName
  visible.value = true
  load()
}

function add() {
  rules.value.push(toEdit({ hostname: '', service: 'http://localhost:8080' }))
}

function move(i: number, d: number) {
  const list = rules.value
  ;[list[i], list[i + d]] = [list[i + d], list[i]]
}

function setOpt(r: EditRule, k: string, v: unknown) {
  if (v === undefined) {
    delete r.originRequest[k]
  } else {
    r.originRequest[k] = v
  }
}

// extraOpts 列出界面未编辑、但会原样保留的回源设置
function extraOpts(r: EditRule) {
  return Object.keys(r.originRequest)
    .filter((k) => !editedOpts.includes(k))
    .join(', ')
}

const services = ['http://localhost:8080', 'https://localhost:443', 'ssh://localhost:22', 'rdp://localhost:3389', 'tcp://localhost:5432', 'http_status:404', 'hello_world']

function serviceSuggestions(q: string, cb: (s: { value: string }[]) => void) {
  cb(services.filter((s) => !q || s.startsWith(q)).map((value) => ({ value })))
}

function catchAllSuggestions(_q: string, cb: (s: { value: string }[]) => void) {
  cb(['http_status:404', 'http_status:503', 'hello_world'].map((value) => ({ value })))
}

function dnsText(d: DNSResult) {
  if (d.status === 'conflict') {
    return t('cloudflared.ingress.dnsConflict', { existing: d.existing })
  }
  if (d.status === 'failed') {
    return t('cloudflared.ingress.dnsFailed', { error: d.error })
  }
  return t(`cloudflared.ingress.dns_${d.status}`)
}

async function save() {
  saving.value = true
  try {
    const body = {
      version: version.value,
      rules: rules.value.map((r) => {
        const out: Rule = { hostname: r.hostname.trim(), path: r.path.trim(), service: r.service.trim() }
        if (Object.keys(r.originRequest).length) {
          out.originRequest = r.originRequest
        }
        return out
      }),
      catchAll: catchAll.value.trim(),
      syncDns: syncDns.value,
    }
    const v = await api.put<IngressView>(`/api/cloudflared/tunnels/${key.value}/ingress`, body)
    apply(v)
    dnsResults.value = v.dns || []
    const problem = dnsResults.value.some((d) => d.status === 'conflict' || d.status === 'failed')
    if (problem) {
      ElMessage.warning(t('cloudflared.ingress.savedDnsIssues'))
    } else {
      ElMessage.success(t('cloudflared.ingress.saved'))
    }
  } catch (e) {
    ElMessage.error((e as Error).message)
  } finally {
    saving.value = false
  }
}

defineExpose({ open })
</script>

<style scoped>
.ing {
  min-height: 120px;
}
.ing-gap {
  margin-bottom: 12px;
}
.ing-rule {
  display: flex;
  gap: 8px;
  align-items: flex-start;
  padding: 10px;
  margin-bottom: 8px;
  border: 1px solid var(--el-border-color-lighter);
  border-radius: 8px;
}
.ing-index {
  width: 20px;
  padding-top: 6px;
  color: var(--el-text-color-secondary);
  text-align: right;
}
.ing-fields {
  flex: 1;
  min-width: 0;
}
.ing-line {
  display: flex;
  gap: 8px;
}
/* 三个输入框按比例分配宽度 */
.ing-line > .ing-host {
  flex: 3 1 0;
  min-width: 0;
}
.ing-line > .ing-path {
  flex: 2 1 0;
  min-width: 0;
}
.ing-line > .ing-svc {
  flex: 3 1 0;
  min-width: 0;
}
.ing-svc .el-autocomplete {
  width: 100%;
}
.ing-opts {
  align-items: center;
  margin-top: 6px;
  flex-wrap: wrap;
}
.ing-hh {
  width: 220px;
}
.ing-extra {
  overflow: hidden;
  font-size: 12px;
  color: var(--el-text-color-secondary);
  text-overflow: ellipsis;
  white-space: nowrap;
}
.ing-actions {
  display: flex;
}
.ing-actions .el-button + .el-button {
  margin-left: 0;
}
.ing-form {
  margin-top: 8px;
}
.ing-dns {
  padding: 8px 12px;
  font-size: 13px;
  border-radius: 8px;
  background: var(--el-fill-color-light);
}
.ing-dns-row {
  display: flex;
  gap: 12px;
  line-height: 1.8;
}
.ing-dns-row.is-conflict,
.ing-dns-row.is-failed {
  color: var(--el-color-warning-dark-2);
}
@media (max-width: 767px) {
  .ing-line:not(.ing-opts) {
    flex-direction: column;
  }
  .ing-line > .ing-host,
  .ing-line > .ing-path,
  .ing-line > .ing-svc {
    flex: none;
  }
  .ing-rule {
    flex-wrap: wrap;
  }
  .ing-hh {
    width: 100%;
  }
}
</style>
