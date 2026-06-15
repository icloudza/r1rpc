import type { ReactNode, ReactElement } from 'react'
import { useEffect, useState } from 'react'
import { Box, Flex, Card, Heading, Text } from '@radix-ui/themes'
import { ActivityLogIcon, CheckCircledIcon, MobileIcon, LayersIcon, LapTimerIcon } from '@radix-ui/react-icons'
import {
  ResponsiveContainer,
  ComposedChart,
  BarChart,
  PieChart,
  Pie,
  Cell,
  Bar,
  Line,
  XAxis,
  YAxis,
  CartesianGrid,
  Tooltip,
  Legend,
} from 'recharts'
import { get } from '../api/client'
import { useFetch } from '../lib/useFetch'
import type { TrendPoint, GroupInfo, Device } from '../types'

const tooltipStyle = {
  background: 'var(--color-panel-solid)',
  border: '1px solid var(--gray-5)',
  borderRadius: 8,
  fontSize: 12,
  boxShadow: 'var(--shadow-3)',
}
const labelStyle = { color: 'var(--gray-12)', fontWeight: 600 }
const axisTick = { fill: 'var(--gray-9)', fontSize: 11 }
const gridStroke = 'var(--gray-a4)'
const noAnim = { isAnimationActive: false as const }

const C = {
  accent: 'var(--accent-9)',
  success: 'var(--green-9)',
  failed: 'var(--red-9)',
  timeout: 'var(--amber-9)',
  latency: 'var(--violet-9)',
}

type ProbeBucket = { t: string; online: number; healthy: number; total: number; maxLatMs: number }
type GroupProbe = { group: string; buckets: ProbeBucket[] }

export default function OverviewPage() {
  const trendsR = useFetch(() => get<{ items: TrendPoint[] }>('/api/metrics/trends?days=7'))
  const groupsR = useFetch(() => get<{ items: GroupInfo[] }>('/api/groups'))
  const devicesR = useFetch(() => get<{ items: Device[] }>('/api/devices'))

  const [probeData, setProbeData] = useState<GroupProbe[]>([])
  useEffect(() => {
    let alive = true
    const load = () =>
      get<{ groups: GroupProbe[] }>('/api/health/probes')
        .then((d) => {
          if (alive) setProbeData(d.groups ?? [])
        })
        .catch(() => {})
    load()
    const id = setInterval(load, 30000)
    return () => {
      alive = false
      clearInterval(id)
    }
  }, [])

  const trends = trendsR.data?.items ?? []
  const groups = groupsR.data?.items ?? []
  const devices = devicesR.data?.items ?? []

  const totalReq = trends.reduce((s, p) => s + p.totalRequests, 0)
  const totalSucc = trends.reduce((s, p) => s + p.successRequests, 0)
  const totalFailed = trends.reduce((s, p) => s + p.failedRequests, 0)
  const totalTimeout = trends.reduce((s, p) => s + p.timeoutRequests, 0)
  const successRate = totalReq ? Math.round((totalSucc / totalReq) * 1000) / 10 : 0
  const avgLatency = totalReq
    ? Math.round(trends.reduce((s, p) => s + p.avgLatencyMs * p.totalRequests, 0) / totalReq)
    : 0
  const onlineDevices = devices.filter((d) => d.status === 'online').length

  const trendData = trends.map((p) => ({
    date: p.statDate.slice(5),
    调用量: p.totalRequests,
    成功率: p.totalRequests ? Math.round(p.successRate * 10) / 10 : null,
    成功: p.successRequests,
    失败: p.failedRequests,
    超时: p.timeoutRequests,
    平均延迟: p.totalRequests ? p.avgLatencyMs : null,
  }))

  const statusPie = [
    { name: '成功', value: totalSucc, color: C.success },
    { name: '失败', value: totalFailed, color: C.failed },
    { name: '超时', value: totalTimeout, color: C.timeout },
  ].filter((x) => x.value > 0)

  const groupBar = [...groups]
    .sort((a, b) => b.requests7d - a.requests7d)
    .slice(0, 6)
    .map((g) => ({ group: g.group, 调用量: g.requests7d }))

  return (
    <Flex direction="column" gap="3">
      {/* 紧凑统计条 */}
      <Card size="2">
        <Flex align="center">
          <MiniStat label="近 7 天调用" value={totalReq} color="blue" icon={<ActivityLogIcon />} />
          <VDivider />
          <MiniStat label="成功率" value={`${successRate}%`} color="green" icon={<CheckCircledIcon />} />
          <VDivider />
          <MiniStat label="平均延迟" value={`${avgLatency} ms`} color="violet" icon={<LapTimerIcon />} />
          <VDivider />
          <MiniStat label="在线设备" value={`${onlineDevices}/${devices.length}`} color="cyan" icon={<MobileIcon />} />
          <VDivider />
          <MiniStat label="分组数" value={groups.length} color="amber" icon={<LayersIcon />} />
        </Flex>
      </Card>

      {/* 趋势(左) + 系统健康(右，独立滚动) */}
      {/* 趋势(左) + 系统健康(右，内容自适应，超出左侧高度才滚) */}
      <Flex gap="3" align="stretch">
        <Card size="2" style={{ flex: 1, minWidth: 0 }}>
          <PanelHead title="调用量 & 成功率趋势" subtitle="柱：调用量 ｜ 线：成功率" />
          <Box mt="3" style={{ height: 240 }}>
            <ResponsiveContainer width="100%" height="100%">
              <ComposedChart data={trendData} margin={{ top: 6, right: 6, bottom: 0, left: -20 }}>
                <defs>
                  <linearGradient id="barFill" x1="0" y1="0" x2="0" y2="1">
                    <stop offset="0%" stopColor={C.accent} stopOpacity={0.95} />
                    <stop offset="100%" stopColor={C.accent} stopOpacity={0.45} />
                  </linearGradient>
                </defs>
                <CartesianGrid vertical={false} stroke={gridStroke} strokeDasharray="3 3" />
                <XAxis dataKey="date" tickLine={false} axisLine={false} tick={axisTick} dy={4} />
                <YAxis yAxisId="left" allowDecimals={false} tickLine={false} axisLine={false} tick={axisTick} width={36} />
                <YAxis yAxisId="right" orientation="right" domain={[0, 100]} unit="%" tickLine={false} axisLine={false} tick={axisTick} width={40} />
                <Tooltip cursor={{ fill: 'var(--gray-a3)' }} contentStyle={tooltipStyle} labelStyle={labelStyle} />
                <Bar yAxisId="left" dataKey="调用量" fill="url(#barFill)" radius={[5, 5, 0, 0]} maxBarSize={40} {...noAnim} />
                <Line yAxisId="right" type="monotone" dataKey="成功率" stroke={C.success} strokeWidth={2} unit="%" dot={{ r: 3, fill: C.success, strokeWidth: 0 }} activeDot={{ r: 5 }} connectNulls {...noAnim} />
              </ComposedChart>
            </ResponsiveContainer>
          </Box>
        </Card>
        <Card size="2" style={{ width: 645, flexShrink: 0, display: 'flex', flexDirection: 'column', overflow: 'hidden' }}>
          <Flex align="baseline" justify="between" mb="2" style={{ flexShrink: 0 }}>
            <PanelHead title="系统健康" subtitle="近 60 分钟 · 30s 刷新" />
            <Flex gap="3" style={{ fontSize: 11, color: 'var(--gray-9)' }}>
              <Flex align="center" gap="1"><Box style={{ width: 8, height: 8, borderRadius: '50%', background: 'var(--green-9)' }} /> 正常</Flex>
              <Flex align="center" gap="1"><Box style={{ width: 8, height: 8, borderRadius: '50%', background: 'var(--amber-9)' }} /> 降级</Flex>
              <Flex align="center" gap="1"><Box style={{ width: 8, height: 8, borderRadius: '50%', background: 'var(--red-9)' }} /> 异常</Flex>
              <Flex align="center" gap="1"><Box style={{ width: 8, height: 8, borderRadius: '50%', background: 'var(--gray-6)' }} /> 无数据</Flex>
            </Flex>
          </Flex>
          <Box style={{ height: 248, overflow: 'auto' }}>
            {groups.length === 0 ? (
              <Flex align="center" justify="center" py="6"><Text size="2" color="gray">暂无分组</Text></Flex>
            ) : (
              <Flex direction="column" gap="4">
                {groups.map((g) => (
                  <HealthRow key={g.group} group={g} buckets={(probeData.find((r) => r.group === g.group)?.buckets) ?? []} />
                ))}
              </Flex>
            )}
          </Box>
        </Card>
      </Flex>

      {/* 状态分布 + 状态构成 + 分组调用量 三列 */}
      <Card size="2">
        <Flex gap="4">
          <Panel title="请求状态分布" subtitle="近 7 天累计" height={190} flex={1} empty={statusPie.length === 0}>
            <PieChart>
              <Pie data={statusPie} dataKey="value" nameKey="name" cx="50%" cy="50%" innerRadius={42} outerRadius={66} paddingAngle={2} stroke="var(--color-panel-solid)" strokeWidth={2} {...noAnim}>
                {statusPie.map((e) => (
                  <Cell key={e.name} fill={e.color} />
                ))}
              </Pie>
              <Tooltip contentStyle={tooltipStyle} />
              <Legend iconType="circle" formatter={(v) => <span style={{ color: 'var(--gray-11)', fontSize: 12 }}>{v}</span>} />
            </PieChart>
          </Panel>
          <VDivider />
          <Panel title="每日请求状态构成" subtitle="成功 / 失败 / 超时" height={190} flex={1}>
            <BarChart data={trendData} margin={{ top: 6, right: 6, bottom: 0, left: -20 }}>
              <CartesianGrid vertical={false} stroke={gridStroke} strokeDasharray="3 3" />
              <XAxis dataKey="date" tickLine={false} axisLine={false} tick={axisTick} dy={4} />
              <YAxis allowDecimals={false} tickLine={false} axisLine={false} tick={axisTick} width={36} />
              <Tooltip cursor={{ fill: 'var(--gray-a3)' }} contentStyle={tooltipStyle} labelStyle={labelStyle} />
              <Legend iconType="circle" formatter={(v) => <span style={{ color: 'var(--gray-11)', fontSize: 11 }}>{v}</span>} />
              <Bar dataKey="成功" stackId="s" fill={C.success} maxBarSize={40} {...noAnim} />
              <Bar dataKey="失败" stackId="s" fill={C.failed} maxBarSize={40} {...noAnim} />
              <Bar dataKey="超时" stackId="s" fill={C.timeout} radius={[4, 4, 0, 0]} maxBarSize={40} {...noAnim} />
            </BarChart>
          </Panel>
          <VDivider />
          <Panel title="分组调用量" subtitle="近 7 天 · Top 6" height={190} flex={1} empty={groupBar.length === 0}>
            <BarChart data={groupBar} layout="vertical" margin={{ top: 4, right: 16, bottom: 0, left: 8 }}>
              <CartesianGrid horizontal={false} stroke={gridStroke} strokeDasharray="3 3" />
              <XAxis type="number" allowDecimals={false} tickLine={false} axisLine={false} tick={axisTick} />
              <YAxis type="category" dataKey="group" tickLine={false} axisLine={false} tick={axisTick} width={72} />
              <Tooltip cursor={{ fill: 'var(--gray-a3)' }} contentStyle={tooltipStyle} labelStyle={labelStyle} />
              <Bar dataKey="调用量" fill={C.accent} radius={[0, 5, 5, 0]} maxBarSize={22} {...noAnim} />
            </BarChart>
          </Panel>
        </Flex>
      </Card>
    </Flex>
  )
}

function VDivider() {
  return <Box style={{ width: 1, alignSelf: 'stretch', background: 'var(--gray-a4)', flexShrink: 0 }} />
}

function PanelHead({ title, subtitle }: { title: string; subtitle?: string }) {
  return (
    <Flex align="baseline" gap="2" wrap="wrap">
      <Heading size="3">{title}</Heading>
      {subtitle && (
        <Text size="1" color="gray">
          {subtitle}
        </Text>
      )}
    </Flex>
  )
}

function Panel({
  title,
  subtitle,
  height,
  flex = 1,
  empty = false,
  children,
}: {
  title: string
  subtitle?: string
  height: number
  flex?: number
  empty?: boolean
  children: ReactNode
}) {
  return (
    <Box style={{ flex, minWidth: 0 }}>
      <PanelHead title={title} subtitle={subtitle} />
      <Box mt="3" style={{ height }}>
        {empty ? (
          // 空状态不能塞进 ResponsiveContainer（会被测量成 0 宽导致文字竖排错位）
          <Flex align="center" justify="center" height="100%">
            <Text size="2" color="gray" style={{ whiteSpace: 'nowrap' }}>
              暂无数据
            </Text>
          </Flex>
        ) : (
          <ResponsiveContainer width="100%" height="100%">
            {children as ReactElement}
          </ResponsiveContainer>
        )}
      </Box>
    </Box>
  )
}

function HealthRow({ group, buckets }: { group: GroupInfo; buckets: ProbeBucket[] }) {
  const SLOW_THRESHOLD = 500
  const totalProbes = buckets.reduce((s, b) => s + b.total, 0)
  const healthyProbes = buckets.reduce((s, b) => s + b.healthy, 0)
  const uptime = totalProbes > 0 ? Math.round((healthyProbes / totalProbes) * 1000) / 10 : null

  const recent5 = buckets.slice(-5)
  const recentTotal = recent5.reduce((s, b) => s + b.total, 0)
  const recentHealthy = recent5.reduce((s, b) => s + b.healthy, 0)
  const recentFailed = recentTotal - recentHealthy

  let badge: { label: string; color: 'green' | 'red' | 'amber' | 'gray' }
  if (!group.enabled) {
    badge = { label: '已禁用', color: 'gray' }
  } else if (group.onlineDevices === 0) {
    badge = group.totalDevices === 0 ? { label: '无设备', color: 'gray' } : { label: '离线', color: 'red' }
  } else if (recentFailed > 0) {
    badge = { label: '异常', color: 'red' }
  } else if (recent5.some((b) => b.total > 0 && b.maxLatMs > SLOW_THRESHOLD)) {
    badge = { label: '降级', color: 'amber' }
  } else {
    badge = { label: '正常', color: 'green' }
  }

  const pills = (buckets.length > 0 ? buckets : Array.from({ length: 60 }, (): ProbeBucket => ({ t: '', online: 0, healthy: 0, total: 0, maxLatMs: 0 }))).map((b, i, arr) => {
    if (b.total > 0 && b.healthy < b.total) return 'var(--red-9)'
    if (b.total > 0 && b.healthy === b.total && b.maxLatMs > SLOW_THRESHOLD) return 'var(--amber-9)'
    if (b.total > 0 && b.healthy === b.total) return 'var(--green-9)'
    // 桶无探针数据：如果之前曾有在线设备（说明后台扫描写过 online>0）但现在掉到 0 → 红色（离线）
    // 否则灰色（无数据/空分组/服务刚启动还没扫描）
    const hadOnline = arr.slice(0, i).some((prev) => prev.online > 0 || prev.total > 0)
    if (hadOnline && b.online === 0) return 'var(--red-9)'
    return 'var(--gray-5)'
  })
  const timeLabels = buckets.length > 0 ? [buckets[0].t, buckets[buckets.length - 1].t] : ['', '']

  return (
    <Box>
      <Flex align="center" justify="between" mb="1">
        <Flex align="center" gap="2">
          <Text weight="medium" size="2">{group.displayName || group.group}</Text>
          <Box style={{ fontSize: 11, padding: '1px 7px', borderRadius: 6, fontWeight: 500, background: `var(--${badge.color}-3)`, color: `var(--${badge.color}-11)` }}>
            {badge.label}
          </Box>
        </Flex>
        <Flex gap="3" style={{ fontSize: 12, color: 'var(--gray-9)' }}>
          <span>设备 {group.onlineDevices}/{group.totalDevices}</span>
          {uptime !== null && group.onlineDevices > 0 && <span style={{ color: uptime >= 99 ? 'var(--green-11)' : uptime >= 90 ? 'var(--amber-11)' : 'var(--red-11)' }}>{uptime}% 健康</span>}
        </Flex>
      </Flex>
      <Box style={{ background: 'var(--gray-a2)', borderRadius: 8, padding: '6px 8px' }}>
        <Flex gap="1" style={{ height: 26, alignItems: 'stretch' }}>
          {pills.map((color, i) => (
            <Box
              key={i}
              title={buckets[i] ? `${buckets[i].t} — 探针 ${buckets[i].healthy}/${buckets[i].total} · 在线 ${buckets[i].online}${buckets[i].maxLatMs > 0 ? ` · ${buckets[i].maxLatMs}ms` : ''}` : ''}
              style={{ flex: 1, minWidth: 0, borderRadius: 3, background: color, opacity: color === 'var(--gray-5)' ? 0.5 : 1, transition: 'opacity .15s' }}
            />
          ))}
        </Flex>
        <Flex justify="between" mt="1" style={{ fontSize: 11, color: 'var(--gray-8)' }}>
          <span>{timeLabels[0]}</span><span>{timeLabels[1]}</span>
        </Flex>
      </Box>
    </Box>
  )
}

function MiniStat({
  label,
  value,
  icon,
  color,
}: {
  label: string
  value: ReactNode
  icon: ReactNode
  color: string
}) {
  return (
    <Flex align="center" gap="2" px="3" py="1" style={{ flex: 1, minWidth: 0 }}>
      <Flex
        align="center"
        justify="center"
        style={{
          width: 34,
          height: 34,
          borderRadius: 9,
          flexShrink: 0,
          background: `var(--${color}-3)`,
          color: `var(--${color}-11)`,
        }}
      >
        {icon}
      </Flex>
      <Flex direction="column" style={{ minWidth: 0 }}>
        <Text size="1" color="gray" truncate>
          {label}
        </Text>
        <Heading size="4">{value}</Heading>
      </Flex>
    </Flex>
  )
}
