import { useState, type FormEvent } from 'react'
import { useNavigate } from 'react-router-dom'
import { Flex, Card, Heading, Text, TextField, Button } from '@radix-ui/themes'
import { post } from '../api/client'
import { auth } from '../auth'
import { notify } from '../lib/toast'
import type { User } from '../types'

export default function LoginPage() {
  const nav = useNavigate()
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [loading, setLoading] = useState(false)

  async function submit(e: FormEvent) {
    e.preventDefault()
    setLoading(true)
    try {
      const data = await post<{ token: string; user: User }>('/api/auth/login', { username, password })
      auth.login(data.token, data.user)
      notify.success('登录成功')
      nav('/overview', { replace: true })
    } catch (e) {
      notify.error(e, '登录失败')
    } finally {
      setLoading(false)
    }
  }

  return (
    <Flex
      align="center"
      justify="center"
      style={{
        height: '100vh',
        background:
          'radial-gradient(1100px 560px at 50% -8%, var(--accent-4), transparent 60%), radial-gradient(900px 500px at 100% 100%, var(--accent-3), transparent 55%), var(--gray-2)',
      }}
    >
      <Card size="4" className="pop-in" style={{ width: 380, boxShadow: 'var(--shadow-5)' }}>
        <Heading size="6" mb="1">
          R1RPC 控制台
        </Heading>
        <Text size="2" color="gray">
          登录管理后台
        </Text>
        <form onSubmit={submit}>
          <Flex direction="column" gap="3" mt="5">
            <label>
              <Text size="2" mb="1" as="div" weight="medium">
                用户名
              </Text>
              <TextField.Root value={username} onChange={(e) => setUsername(e.target.value)} placeholder="请输入用户名" size="3" />
            </label>
            <label>
              <Text size="2" mb="1" as="div" weight="medium">
                密码
              </Text>
              <TextField.Root
                type="password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                placeholder="请输入密码"
                size="3"
              />
            </label>
            <Button type="submit" loading={loading} size="3" mt="2">
              登录
            </Button>
          </Flex>
        </form>
      </Card>
    </Flex>
  )
}
