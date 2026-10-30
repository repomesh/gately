import { ref } from 'vue'
import { api } from '../api'
import type { FileUser } from '../components/FileUsers.vue'

// SMBIssue 是无法用于 SMB 的共享用户及原因代码（name / bcrypt / share）
export interface SMBIssue {
  name: string
  reason: string
}

// FileService 是一个文件服务对共享用户的使用情况（已保存的设置），已关闭的模块不在列表中
export interface FileService {
  id: string
  enable: boolean
  // sharedUsers 为是否允许共享用户登录
  sharedUsers: boolean
  // localUsers 为本地用户名，与之同名的共享用户在该服务中只按本地用户校验
  localUsers: string[]
  // caseInsensitive 为 true 时按不区分大小写比较用户名（SMB）
  caseInsensitive?: boolean
  // issues 为已保存的共享用户中无法用于该服务的用户
  issues?: SMBIssue[]
}

interface Reply {
  users: FileUser[] | null
  // selfService 为是否允许共享用户自助修改密码
  selfService?: boolean
  services?: FileService[]
  error?: string
}

// useSharedFileUsers 读取与保存 WebDAV、FTP、SMB 共用的文件服务共享用户（/api/fileusers）。
// 共享用户页面用它编辑列表，各服务页面用它显示摘要与 SMB 连接地址。
export function useSharedFileUsers() {
  const users = ref<FileUser[]>([])
  const services = ref<FileService[]>([])
  // loadError 为服务端读取到的共享用户配置未通过校验的错误，此时共享用户都不能登录，保存一份有效的列表后恢复
  const loadError = ref('')
  // savedUsers 为已保存的共享用户（不随表单编辑变化），用于展示连接地址、数量等以已保存配置为准的内容
  const savedUsers = ref<FileUser[]>([])
  const loaded = ref(false)
  // selfService 为是否允许共享用户在 Web 服务站点的自助页面修改密码，随列表一起保存
  const selfService = ref(false)
  // saved 为最近一次读取或保存的内容，用于判断是否有未保存的修改
  let saved = ''

  function apply(r: Reply) {
    users.value = (r.users ?? []).map((u) => ({ ...u, mounts: u.mounts ?? [] }))
    services.value = r.services ?? []
    loadError.value = r.error ?? ''
    selfService.value = !!r.selfService
    saved = snapshot()
    savedUsers.value = JSON.parse(saved).users
    loaded.value = true
  }

  async function load() {
    apply(await api.get<Reply>('/api/fileusers'))
  }

  async function save() {
    apply(await api.put<Reply>('/api/fileusers', { users: users.value, selfService: selfService.value }))
  }

  // snapshot 返回列表与开关的快照，用于判断是否有未保存的修改
  function snapshot(): string {
    return JSON.stringify({ users: users.value, selfService: selfService.value })
  }

  // dirty 判断列表或开关是否有未保存的修改
  function dirty(): boolean {
    return snapshot() !== saved
  }

  // service 返回指定服务的使用情况，模块已关闭时为 undefined
  function service(id: string): FileService | undefined {
    return services.value.find((s) => s.id === id)
  }

  return { users, savedUsers, selfService, services, loadError, loaded, load, save, dirty, service }
}

// issueText 按原因代码返回无法用于 SMB 的说明，未知代码按共享名问题处理
export function issueText(t: (key: string, values?: Record<string, unknown>) => string, is: SMBIssue): string {
  switch (is.reason) {
    case 'name':
      return t('sharedFileUsers.issue.name', { name: is.name })
    case 'bcrypt':
      return t('sharedFileUsers.issue.bcrypt', { name: is.name })
    default:
      return t('sharedFileUsers.issue.share', { name: is.name })
  }
}

// shadowedNames 返回与本地用户同名、在该服务中只按本地用户校验的共享用户名；caseInsensitive 为 true 时不区分大小写（SMB）
export function shadowedNames(users: FileUser[], localNames: string[], caseInsensitive?: boolean): string[] {
  const norm = (s: string) => (caseInsensitive ? s.trim().toLowerCase() : s.trim())
  const local = new Set(localNames.map(norm))
  return users.map((u) => u.name.trim()).filter((n) => n && local.has(norm(n)))
}
