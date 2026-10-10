import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createBrowserRouter, RouterProvider } from 'react-router-dom'
import '@fontsource-variable/bricolage-grotesque'
import '@fontsource/ibm-plex-sans/400.css'
import '@fontsource/ibm-plex-sans/500.css'
import '@fontsource/ibm-plex-sans/600.css'
import '@fontsource/jetbrains-mono/400.css'
import '@fontsource/jetbrains-mono/600.css'
import './index.css'
import { applyInitialTheme } from '@/lib/theme'
import { ApiError } from '@/lib/api'
import { AppShell } from '@/components/AppShell'
import { LoginGate } from '@/components/LoginGate'
import { OverviewPage } from '@/pages/Overview'
import { AccountsPage } from '@/pages/Accounts'
import { ModelsPage } from '@/pages/Models'
import { Placeholder } from '@/pages/Placeholder'

applyInitialTheme()

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      retry: (n, err) => !(err instanceof ApiError && err.status === 401) && n < 2,
      refetchOnWindowFocus: false,
    },
  },
})

const router = createBrowserRouter([
  {
    element: <AppShell />,
    children: [
      { path: '/', element: <OverviewPage /> },
      { path: '/accounts', element: <AccountsPage /> },
      { path: '/models', element: <ModelsPage /> },
      {
        path: '/credits',
        element: <Placeholder phase="P4" title="积分中心" items={['积分包到期日历与浪费预警', '账号消耗速度与耗尽预测', '选号策略配置与决策回放']} />,
      },
      {
        path: '/gateway',
        element: <Placeholder phase="P3" title="接入与密钥" items={['下游 API Key：模型白名单、预算、RPM', 'ttswitch 接入向导与配置一键复制']} />,
      },
      { path: '/usage', element: <Placeholder phase="P5" title="用量分析" items={['Token / 积分时序', '按账号、模型、Key 维度拆分']} /> },
      { path: '/logs', element: <Placeholder phase="P5" title="请求日志" items={['请求记录筛选与详情', '运行日志分频道']} /> },
      { path: '/tasks', element: <Placeholder phase="P6" title="任务" items={['签到 / 保活 / 余额刷新等定时任务', '成长任务中心']} /> },
      { path: '/settings', element: <Placeholder phase="P5" title="设置" items={['服务与安全', '各平台配置']} /> },
    ],
  },
])

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <LoginGate>
        <RouterProvider router={router} />
      </LoginGate>
    </QueryClientProvider>
  </StrictMode>,
)
