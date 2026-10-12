#!/usr/bin/env python3
"""生成发布版变更记录，去掉内部开发联调用的代号。

源码中的 CHANGELOG.md 保留完整、准确的变更史；随 Release 压缩包分发给用户
的版本则去掉内部代号，避免用户误以为产品附带模拟 / 演示能力。

用法：sanitize_changelog.py <输入> <输出>
输出中若仍残留任何 mock 字样（不区分大小写）则以非零码退出，防止后续新增
内容绕过脱敏直接流进发布包（CI 会因此失败）。
"""

import re
import sys

# 按顺序执行的片段替换：先处理整句，再处理零散标识。
PAIRS = [
    (
        "开发联调用的 `mock` 平台（3 个演示账号与 `mock-echo` / `mock-think` 两个模型）",
        "开发联调用的内部演示平台（3 个演示账号与两个演示模型）",
    ),
    ("`providers.mock` 配置项", "对应配置项"),
    (
        "网关测试改用 `internal/gateway/fakeprovider_test.go` 中的测试替身",
        "网关测试改用测试文件内的测试替身",
    ),
    ("- mock 平台", "- 内部演示平台"),
]

# 兜底：上述规则未覆盖到的残留标识统一中性化。
FALLBACKS = [
    (r"`?mock-echo`?", "回显模型"),
    (r"`?mock-think`?", "推理模型"),
    (r"`?providers\.mock`?", "对应配置项"),
    (r"\bmock\b", "内部演示"),
    (r"\bMOCK\b", "内部演示"),
]


def sanitize(text: str) -> str:
    for old, new in PAIRS:
        text = text.replace(old, new)
    for pattern, new in FALLBACKS:
        text = re.sub(pattern, new, text)
    return text


def main() -> int:
    if len(sys.argv) != 3:
        print("usage: sanitize_changelog.py <input> <output>", file=sys.stderr)
        return 2

    with open(sys.argv[1], encoding="utf-8") as f:
        result = sanitize(f.read())

    leftover = [w for w in ("mock", "MOCK", "Mock") if w in result]
    if leftover:
        print(f"脱敏未覆盖干净，残留：{leftover}", file=sys.stderr)
        for i, line in enumerate(result.splitlines(), 1):
            if re.search(r"(?i)mock", line):
                print(f"  L{i}: {line}", file=sys.stderr)
        return 1

    with open(sys.argv[2], "w", encoding="utf-8", newline="\n") as f:
        f.write(result)
    print(f"已生成脱敏变更记录：{sys.argv[2]}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
