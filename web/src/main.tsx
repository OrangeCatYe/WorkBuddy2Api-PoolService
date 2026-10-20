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
import { TasksPage } from '@/pages/Tasks'
import { SettingsPage } from '@/pages/Settings'
import { GatewayPage } from '@/pages/Gateway'
import { LogsPage } from '@/pages/Logs'
import { UsagePage } from '@/pages/Usage'
import { CreditsPage } from '@/pages/Credits'
import { AgentsPage } from '@/pages/Agents'
import { ToastProvider } from '@/components/overlay'

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
      { path: '/credits', element: <CreditsPage /> },
      { path: '/gateway', element: <GatewayPage /> },
      { path: '/agents', element: <AgentsPage /> },
      { path: '/usage', element: <UsagePage /> },
      { path: '/logs', element: <LogsPage /> },
      { path: '/tasks', element: <TasksPage /> },
      { path: '/settings', element: <SettingsPage /> },
    ],
  },
])

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <ToastProvider>
        <LoginGate>
          <RouterProvider router={router} />
        </LoginGate>
      </ToastProvider>
    </QueryClientProvider>
  </StrictMode>,
)
