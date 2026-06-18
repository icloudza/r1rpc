import { useState, type ComponentProps } from 'react'
import {
  Flex,
  Card,
  Table,
  Button,
  Badge,
  Dialog,
  AlertDialog,
  TextField,
  TextArea,
  Text,
  Switch,
  Select,
  Spinner,
  Code,
  IconButton,
} from '@radix-ui/themes'
import { PlusIcon, ReloadIcon, CopyIcon, TrashIcon, Pencil1Icon } from '@radix-ui/react-icons'
import { get, post, patch, del } from '../api/client'
import { useFetch } from '../lib/useFetch'
import { notify } from '../lib/toast'
import type { GroupInfo } from '../types'

type BadgeColor = ComponentProps<typeof Badge>['color']
const statusColor: Record<string, BadgeColor> = {
  online: 'green',
  offline: 'gray',
  no_device: 'gray',
  disabled: 'red',
  stale: 'amber',
}
const statusCn: Record<string, string> = {
  online: '在线',
  offline: '离线',
  no_device: '无设备',
  disabled: '已禁用',
  stale: '不活跃',
}

function copyToClipboard(text: string) {
  if (navigator.clipboard?.writeText) {
    navigator.clipboard.writeText(text).catch(() => fallbackCopy(text))
  } else {
    fallbackCopy(text)
  }
}
function fallbackCopy(text: string) {
  const ta = document.createElement('textarea')
  ta.value = text
  ta.style.cssText = 'position:fixed;opacity:0'
  document.body.appendChild(ta)
  ta.select()
  document.execCommand('copy')
  document.body.removeChild(ta)
}

export default function GroupsPage() {
  const { data, loading, reload } = useFetch(() => get<{ items: GroupInfo[] }>('/api/groups'))
  const groups = data?.items ?? []

  async function toggle(g: GroupInfo) {
    try {
      await patch(`/api/groups/${encodeURIComponent(g.group)}/status`, { enabled: !g.enabled })
      notify.success(g.enabled ? `已停用分组 ${g.group}` : `已启用分组 ${g.group}`)
    } catch (e) {
      notify.error(e)
    } finally {
      reload()
    }
  }

  function refresh() {
    reload()
    notify.success('已刷新')
  }

  return (
    <Flex direction="column" gap="4">
      <Flex justify="between" align="center">
        <Text size="2" color="gray">
          设备池 / 路由命名空间
        </Text>
        <Flex gap="2">
          <Button variant="soft" color="gray" onClick={refresh}>
            <ReloadIcon /> 刷新
          </Button>
          <CreateGroupDialog onCreated={reload} />
        </Flex>
      </Flex>

      <Card size="2">
        {loading ? (
          <Flex justify="center" p="6">
            <Spinner size="3" />
          </Flex>
        ) : (
          <Table.Root variant="surface">
            <Table.Header>
              <Table.Row>
                <Table.ColumnHeaderCell>分组 / 路由</Table.ColumnHeaderCell>
                <Table.ColumnHeaderCell>状态</Table.ColumnHeaderCell>
                <Table.ColumnHeaderCell>设备(在线/总)</Table.ColumnHeaderCell>
                <Table.ColumnHeaderCell>近 7 天调用</Table.ColumnHeaderCell>
                <Table.ColumnHeaderCell>成功率</Table.ColumnHeaderCell>
                <Table.ColumnHeaderCell>备注</Table.ColumnHeaderCell>
                <Table.ColumnHeaderCell>设备密钥</Table.ColumnHeaderCell>
                <Table.ColumnHeaderCell>调用鉴权</Table.ColumnHeaderCell>
                <Table.ColumnHeaderCell>启用</Table.ColumnHeaderCell>
                <Table.ColumnHeaderCell>操作</Table.ColumnHeaderCell>
              </Table.Row>
            </Table.Header>
            <Table.Body>
              {groups.map((g) => (
                <Table.Row key={g.group} align="center">
                  <Table.RowHeaderCell>
                    <Flex direction="column" gap="1" align="start">
                      <Text weight="medium">{g.displayName || g.group}</Text>
                      <Code variant="ghost" size="1" color="gray">
                        {g.group}
                      </Code>
                    </Flex>
                  </Table.RowHeaderCell>
                  <Table.Cell>
                    <Badge color={statusColor[g.status] ?? 'gray'} variant="soft">
                      {statusCn[g.status] ?? g.statusLabel ?? g.status}
                    </Badge>
                  </Table.Cell>
                  <Table.Cell>
                    {g.onlineDevices} / {g.totalDevices}
                  </Table.Cell>
                  <Table.Cell>{g.requests7d}</Table.Cell>
                  <Table.Cell>{g.requests7d ? `${g.successRate.toFixed(1)}%` : '—'}</Table.Cell>
                  <Table.Cell>
                    <Text color="gray">{g.notes || '—'}</Text>
                  </Table.Cell>
                  <Table.Cell>
                    <DeviceKeyCell group={g} onRotated={reload} />
                  </Table.Cell>
                  <Table.Cell>
                    <AuthCell group={g} onRotated={reload} />
                  </Table.Cell>
                  <Table.Cell>
                    <Switch checked={g.enabled} onCheckedChange={() => toggle(g)} />
                  </Table.Cell>
                  <Table.Cell>
                    <Flex gap="2">
                      <EditGroupButton group={g} onUpdated={reload} />
                      <DeleteGroupButton group={g} onDeleted={reload} />
                    </Flex>
                  </Table.Cell>
                </Table.Row>
              ))}
              {groups.length === 0 && (
                <Table.Row>
                  <Table.Cell colSpan={10}>
                    <Text color="gray">暂无分组，点击右上角「新建分组」</Text>
                  </Table.Cell>
                </Table.Row>
              )}
            </Table.Body>
          </Table.Root>
        )}
      </Card>
    </Flex>
  )
}

function DeviceKeyCell({ group, onRotated }: { group: GroupInfo; onRotated: () => void }) {
  const [busy, setBusy] = useState(false)
  const key = group.deviceKey || ''

  async function rotate() {
    setBusy(true)
    try {
      await post(`/api/groups/${encodeURIComponent(group.group)}/device-key`, {})
      notify.success(`已轮换 ${group.group} 的设备密钥`)
      onRotated()
    } catch (e) {
      notify.error(e)
    } finally {
      setBusy(false)
    }
  }

  function copy() {
    if (!key) return
    copyToClipboard(key)
    notify.success('设备密钥已复制')
  }

  return (
    <Flex align="center" gap="1">
      <Code variant="ghost" size="1" title={key}>
        {key ? `${key.slice(0, 12)}…` : '—'}
      </Code>
      <IconButton size="1" variant="ghost" color="gray" title="复制密钥" disabled={!key} onClick={copy}>
        <CopyIcon />
      </IconButton>
      <AlertDialog.Root>
        <AlertDialog.Trigger>
          <IconButton size="1" variant="ghost" color="gray" title="轮换密钥" loading={busy}>
            <ReloadIcon />
          </IconButton>
        </AlertDialog.Trigger>
        <AlertDialog.Content maxWidth="440px">
          <AlertDialog.Title>轮换设备密钥</AlertDialog.Title>
          <AlertDialog.Description size="2">
            确认为分组「{group.group}」重新生成设备密钥？旧密钥将 <strong>立即失效</strong>，所有使用旧密钥的设备需更新为新密钥后才能重新登录。
          </AlertDialog.Description>
          <Flex gap="3" mt="4" justify="end">
            <AlertDialog.Cancel>
              <Button variant="soft" color="gray">
                取消
              </Button>
            </AlertDialog.Cancel>
            <AlertDialog.Action>
              <Button color="red" onClick={rotate}>
                确认轮换
              </Button>
            </AlertDialog.Action>
          </Flex>
        </AlertDialog.Content>
      </AlertDialog.Root>
    </Flex>
  )
}

function AuthCell({ group, onRotated }: { group: GroupInfo; onRotated: () => void }) {
  const [busy, setBusy] = useState(false)
  const key = group.apiKey || ''

  async function rotate() {
    setBusy(true)
    try {
      await post(`/api/groups/${encodeURIComponent(group.group)}/api-key`, {})
      notify.success(`已轮换 ${group.group} 的 API Key`)
      onRotated()
    } catch (e) {
      notify.error(e)
    } finally {
      setBusy(false)
    }
  }

  function copy() {
    if (!key) return
    copyToClipboard(key)
    notify.success('API Key 已复制')
  }

  if (group.authMode !== 'apikey') {
    return (
      <Badge color="gray" variant="soft">
        免鉴权
      </Badge>
    )
  }

  return (
    <Flex align="center" gap="1">
      <Badge color="amber" variant="soft">
        apikey
      </Badge>
      <Code variant="ghost" size="1" title={key}>
        {key ? `${key.slice(0, 10)}…` : '—'}
      </Code>
      <IconButton size="1" variant="ghost" color="gray" title="复制 API Key" disabled={!key} onClick={copy}>
        <CopyIcon />
      </IconButton>
      <AlertDialog.Root>
        <AlertDialog.Trigger>
          <IconButton size="1" variant="ghost" color="gray" title="轮换 API Key" loading={busy}>
            <ReloadIcon />
          </IconButton>
        </AlertDialog.Trigger>
        <AlertDialog.Content maxWidth="440px">
          <AlertDialog.Title>轮换 API Key</AlertDialog.Title>
          <AlertDialog.Description size="2">
            确认为分组「{group.group}」重新生成调用 API Key？旧 key 立即失效，所有调用方需更新。
          </AlertDialog.Description>
          <Flex gap="3" mt="4" justify="end">
            <AlertDialog.Cancel>
              <Button variant="soft" color="gray">
                取消
              </Button>
            </AlertDialog.Cancel>
            <AlertDialog.Action>
              <Button color="red" onClick={rotate}>
                确认轮换
              </Button>
            </AlertDialog.Action>
          </Flex>
        </AlertDialog.Content>
      </AlertDialog.Root>
    </Flex>
  )
}

function DeleteGroupButton({ group, onDeleted }: { group: GroupInfo; onDeleted: () => void }) {
  const [busy, setBusy] = useState(false)
  const hasOnline = group.onlineDevices > 0

  async function remove() {
    setBusy(true)
    try {
      await del(`/api/groups/${encodeURIComponent(group.group)}`)
      notify.success(`已删除分组 ${group.group}`)
      onDeleted()
    } catch (e) {
      notify.error(e)
    } finally {
      setBusy(false)
    }
  }

  if (hasOnline) {
    return (
      <Button size="1" variant="ghost" color="gray" disabled title="分组下有在线设备，无法删除">
        <TrashIcon /> 删除
      </Button>
    )
  }

  return (
    <AlertDialog.Root>
      <AlertDialog.Trigger>
        <Button size="1" variant="ghost" color="red">
          <TrashIcon /> 删除
        </Button>
      </AlertDialog.Trigger>
      <AlertDialog.Content maxWidth="400px">
        <AlertDialog.Title>删除分组</AlertDialog.Title>
        <AlertDialog.Description size="2">
          确认删除分组「{group.group}」？删除后该分组不能再被设备登录或 RPC 调用；历史调用记录不受影响。
        </AlertDialog.Description>
        <Flex gap="3" mt="4" justify="end">
          <AlertDialog.Cancel>
            <Button variant="soft" color="gray">
              取消
            </Button>
          </AlertDialog.Cancel>
          <AlertDialog.Action>
            <Button color="red" loading={busy} onClick={remove}>
              删除
            </Button>
          </AlertDialog.Action>
        </Flex>
      </AlertDialog.Content>
    </AlertDialog.Root>
  )
}

function EditGroupButton({ group, onUpdated }: { group: GroupInfo; onUpdated: () => void }) {
  const [open, setOpen] = useState(false)
  const [displayName, setDisplayName] = useState(group.displayName || '')
  const [notes, setNotes] = useState(group.notes || '')
  const [authMode, setAuthMode] = useState(group.authMode || 'none')
  const [busy, setBusy] = useState(false)

  function onOpenChange(o: boolean) {
    setOpen(o)
    if (o) {
      setDisplayName(group.displayName || '')
      setNotes(group.notes || '')
      setAuthMode(group.authMode || 'none')
    }
  }

  async function submit() {
    setBusy(true)
    try {
      await patch(`/api/groups/${encodeURIComponent(group.group)}`, {
        displayName: displayName.trim(),
        notes: notes.trim(),
        authMode,
      })
      notify.success(`已更新分组 ${group.group}`)
      setOpen(false)
      onUpdated()
    } catch (e) {
      notify.error(e)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange}>
      <Dialog.Trigger>
        <Button size="1" variant="ghost" color="gray">
          <Pencil1Icon /> 编辑
        </Button>
      </Dialog.Trigger>
      <Dialog.Content maxWidth="420px">
        <Dialog.Title>编辑分组 · {group.group}</Dialog.Title>
        <Dialog.Description size="2" color="gray" mb="4">
          路由 <Code variant="ghost">{group.group}</Code> 不可改（设备和调用方都依赖它）；可改中文名和备注。
        </Dialog.Description>
        <Flex direction="column" gap="3">
          <label>
            <Text size="2" mb="1" as="div" weight="medium">
              中文名
            </Text>
            <TextField.Root value={displayName} onChange={(e) => setDisplayName(e.target.value)} placeholder="例如 示例分组" />
          </label>
          <label>
            <Text size="2" mb="1" as="div" weight="medium">
              调用鉴权
            </Text>
            <Select.Root value={authMode} onValueChange={setAuthMode}>
              <Select.Trigger style={{ width: '100%' }} />
              <Select.Content>
                <Select.Item value="none">免鉴权</Select.Item>
                <Select.Item value="apikey">API Key</Select.Item>
              </Select.Content>
            </Select.Root>
            <Text size="1" color="gray" mt="1" as="div">
              切到 API Key 会自动生成密钥；调用时需在请求头带 X-API-Key。
            </Text>
          </label>
          <label>
            <Text size="2" mb="1" as="div" weight="medium">
              备注
            </Text>
            <TextArea value={notes} onChange={(e) => setNotes(e.target.value)} />
          </label>
        </Flex>
        <Flex gap="3" mt="4" justify="end">
          <Dialog.Close>
            <Button variant="soft" color="gray">
              取消
            </Button>
          </Dialog.Close>
          <Button onClick={submit} loading={busy}>
            保存
          </Button>
        </Flex>
      </Dialog.Content>
    </Dialog.Root>
  )
}

function CreateGroupDialog({ onCreated }: { onCreated: () => void }) {
  const [open, setOpen] = useState(false)
  const [displayName, setDisplayName] = useState('')
  const [name, setName] = useState('')
  const [notes, setNotes] = useState('')
  const [authMode, setAuthMode] = useState('none')
  const [busy, setBusy] = useState(false)

  async function submit() {
    if (!name.trim()) {
      notify.error('请输入路由名')
      return
    }
    if (!/^[A-Za-z0-9_-]+$/.test(name.trim())) {
      notify.error('路由名只能用英文字母、数字、- 或 _')
      return
    }
    setBusy(true)
    try {
      await post('/api/groups', { name: name.trim(), displayName: displayName.trim(), authMode, notes: notes.trim() })
      notify.success(`已创建分组 ${name.trim()}`)
      setOpen(false)
      setName('')
      setDisplayName('')
      setNotes('')
      setAuthMode('none')
      onCreated()
    } catch (e) {
      notify.error(e)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog.Root open={open} onOpenChange={setOpen}>
      <Dialog.Trigger>
        <Button>
          <PlusIcon /> 新建分组
        </Button>
      </Dialog.Trigger>
      <Dialog.Content maxWidth="420px">
        <Dialog.Title>新建分组</Dialog.Title>
        <Dialog.Description size="2" color="gray" mb="4">
          分组 = 设备池 / 路由命名空间。中文名仅作展示，路由（英文）是调用与设备登录寻址用的 key。
        </Dialog.Description>
        <Flex direction="column" gap="3">
          <label>
            <Text size="2" mb="1" as="div" weight="medium">
              中文名（可选）
            </Text>
            <TextField.Root value={displayName} onChange={(e) => setDisplayName(e.target.value)} placeholder="例如 示例分组" />
          </label>
          <label>
            <Text size="2" mb="1" as="div" weight="medium">
              路由（英文，不可改）
            </Text>
            <TextField.Root value={name} onChange={(e) => setName(e.target.value)} placeholder="例如 xhs" />
          </label>
          <label>
            <Text size="2" mb="1" as="div" weight="medium">
              调用鉴权
            </Text>
            <Select.Root value={authMode} onValueChange={setAuthMode}>
              <Select.Trigger style={{ width: '100%' }} />
              <Select.Content>
                <Select.Item value="none">免鉴权</Select.Item>
                <Select.Item value="apikey">API Key</Select.Item>
              </Select.Content>
            </Select.Root>
            <Text size="1" color="gray" mt="1" as="div">
              选 API Key 会自动生成密钥；调用时需在请求头带 X-API-Key。
            </Text>
          </label>
          <label>
            <Text size="2" mb="1" as="div" weight="medium">
              备注（可选）
            </Text>
            <TextArea value={notes} onChange={(e) => setNotes(e.target.value)} />
          </label>
        </Flex>
        <Flex gap="3" mt="4" justify="end">
          <Dialog.Close>
            <Button variant="soft" color="gray">
              取消
            </Button>
          </Dialog.Close>
          <Button onClick={submit} loading={busy}>
            创建
          </Button>
        </Flex>
      </Dialog.Content>
    </Dialog.Root>
  )
}
