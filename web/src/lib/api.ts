const KEY_STORAGE = 'poolsvc.adminKey'

export class ApiError extends Error {
  constructor(
    public status: number,
    message: string,
  ) {
    super(message)
  }
}

export const adminKey = {
  get: () => localStorage.getItem(KEY_STORAGE) ?? '',
  set: (v: string) => localStorage.setItem(KEY_STORAGE, v),
  clear: () => localStorage.removeItem(KEY_STORAGE),
}

type Listener = () => void
const unauthorizedListeners = new Set<Listener>()
export const onUnauthorized = (fn: Listener) => {
  unauthorizedListeners.add(fn)
  return () => {
    unauthorizedListeners.delete(fn)
  }
}

export async function api<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers)
  const key = adminKey.get()
  if (key) headers.set('Authorization', `Bearer ${key}`)
  if (init.body && !headers.has('Content-Type')) headers.set('Content-Type', 'application/json')
  const res = await fetch(path, { ...init, headers })
  if (res.status === 401) {
    unauthorizedListeners.forEach((fn) => fn())
    throw new ApiError(401, '未授权：请输入管理密钥')
  }
  const text = await res.text()
  const data = text ? JSON.parse(text) : undefined
  if (!res.ok) {
    const msg = data?.error?.message ?? data?.error ?? res.statusText
    throw new ApiError(res.status, String(msg))
  }
  return data as T
}

export const post = <T>(path: string, body?: unknown) =>
  api<T>(path, { method: 'POST', body: body === undefined ? undefined : JSON.stringify(body) })
