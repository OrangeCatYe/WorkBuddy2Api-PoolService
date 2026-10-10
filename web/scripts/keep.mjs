import { writeFileSync } from 'node:fs'

// vite emptyOutDir 会清掉占位文件；Go embed 需要目录在未构建时也存在。
writeFileSync(new URL('../../internal/webui/dist/.gitkeep', import.meta.url), '')
