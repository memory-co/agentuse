# UI 基础组件

本目录采用 shadcn/ui 官方 New York 组件源码，使用 Radix primitives，兼容项目现有的 React 18 与 Tailwind CSS 3。源码来自 `https://ui.shadcn.com/r/styles/new-york/<组件名>.json`，许可证见 [LICENSE.md](LICENSE.md)。

业务页面统一从 `@/components/ui/*` 引用通用控件。组件源码和主题从 memory.talk 的前端(`memorytalk/frontend`)拷来，保持两边一致。

- `components.json` 声明组件风格、路径别名和 Tailwind 配置。
- `src/index.css` 的主题变量定义颜色与圆角；业务布局放在 `@layer components`，不覆盖全局按钮、输入框等原生元素。
- 按钮通过 `variant` / `size` 表达用途，破坏性确认使用 AlertDialog(`components/Shared.tsx` 的 `Confirm`)。
- 跟随系统深浅色(`main.tsx` 给 `<html>` 加 `.dark`)。Sonner 直接使用应用主题，不引入 Next.js 或 next-themes。

本地适配包括中文无障碍文案、小屏弹窗尺寸和遮罩透明度，更新官方组件时需保留。

新增组件可以在前端目录执行 `npx shadcn@latest add <组件名>`。审查生成的源码与依赖，保持 React 18 / Tailwind 3 兼容；已经修改过的组件不要直接整体覆盖。
