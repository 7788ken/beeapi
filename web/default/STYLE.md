# beeapi 风格手册

beeapi 前端有**两套并行的视觉系统**。动手前先确认改的是哪一类页面，再翻对应章节。
编辑本目录时以本文件为准。

| 面 | 范围 | 主色 | 看哪章 |
|---|---|---|---|
| **控制台** | 登录后的应用内页面（仪表盘、渠道、日志、设置……） | Stripe 紫 `#635bff` | 第二章 |
| **落地页** | 对外营销页（首页、关于页及同族页面） | 品牌深藏蓝 `#0b1c47` + 亮蓝 `#1b55e2` | 第三章 |

**判断口令**：DOM 挂在 `.home-type` 下面的，按落地页规则；其余一律按控制台规则。
两套只在**第一章的共同底线**上重合，颜色、圆角、阴影、字号各走各的，互相不要搬。

> 为什么是两套：控制台是数据界面，要的是密度和可信；落地页是第一印象，要的是气场和品牌识别。
> 一套 token 同时喂这两种目标，结果是两边都不像。

---

## 一、共同底线（两套都必须遵守）

1. 过渡只用 `ease-out`。禁止 `ease` 和 `ease-in-out`。
2. 悬停只上浮，不放大。禁止 `hover:scale-*`。按下用 `active:scale-[0.98]`。
3. 可交互元素必须有 hover、active、focus 三态，并提供 `motion-reduce` 降级。
4. 焦点环必须可见，用自定义 `focus-visible:shadow-[0_0_0_3px_...]`，不要靠浏览器默认描边。
5. 状态不能只靠颜色传达，必须同时有文字或图标。
6. 不嵌套卡片，不用单侧粗边框，不用玻璃态，不用渐变文字。
7. 不引入 Inter、Roboto、Geist。沿用应用已加载的字体。
8. 手机、平板、桌面都不能横向溢出。宽表格、图表、代码块各自套 `overflow-x-auto`，并给该元素加 `min-w-0`（否则它会撑宽父轨道而不是滚动）。
9. 字距（`tracking-*`）是给拉丁文用的。**中文上禁止超过 `0.1em`**，否则词会被拆散成「一 个 接 口」。需要那种字距质感时，改用描边胶囊。

---

## 二、控制台：Stripe 风格

主色是 Stripe 紫 `#635bff`，全站 token 定义在 `src/styles/theme.css`。页面要像支付产品和开发者工具：可信、克制、有网格和代码质感。

### 冲突怎么判

本章后半保留了原始 token。和下面几条冲突时，以这几条为准。

1. 禁止项高于示例模板。
2. 按钮静止态必须带内高光：`inset_0_1px_0_rgba(255,255,255,0.2)`。写在自定义阴影里，禁止工具类 `shadow-inner`。
3. 按钮 `duration-[300ms]`，卡片 `duration-[400ms]`。
4. 网格是 40px 浅紫线，不是紫到青的渐变铺底。禁止 `from-[#635bff] to-[#00d4ff]` 这一类渐变背景。
5. `#635bff` 底上只放白字。禁止在这块彩色背景上使用灰色文字。
6. 控制台是数据界面。不要用落地页的 `text-5xl`、`text-7xl` 和 `py-16`。
7. 正向 `text-[#635bff]`，异常 `text-[#0a2540]` 加粗，持平 `text-gray-600`。禁止 `bg-red-*`、`bg-yellow-*`、`bg-pink`、`text-pink`。异常色就是 `--destructive`：浅色 `#0a2540`，暗色是近白正文色 `#f6f9fc`（不能用主色紫，否则失败数看着像被选中）。拿它当底色（危险按钮、失败徽标）时，文字用 `text-destructive-foreground`（浅色白、暗色深藏蓝），不要写死 `text-white`。

### 必须遵守

- 主色 `bg-[#635bff]`、`text-[#635bff]`
- 标题和正文主色 `text-[#0a2540]`
- 页面底 `bg-[#f6f9fc]`，卡片 `bg-white`
- 圆角只用 `rounded-lg` 或 `rounded-xl`
- 卡片多层阴影：`shadow-[0_2px_4px_rgba(0,0,0,0.04),0_8px_16px_rgba(0,0,0,0.08)]`
- 每个控制台页面都要有网格背景
- 代码和接口地址使用深色底 `bg-[#0a2540]`、`font-mono text-sm`、白字

### 网格背景

```css
background-color: #f6f9fc;
background-image:
  linear-gradient(to right, rgba(99, 91, 255, 0.1) 1px, transparent 1px),
  linear-gradient(to bottom, rgba(99, 91, 255, 0.1) 1px, transparent 1px);
background-size: 40px 40px;
```

### 主按钮

```html
<button class="px-6 py-3 bg-[#635bff] rounded-lg text-white font-medium shadow-[0_2px_5px_rgba(99,91,255,0.4),inset_0_1px_0_rgba(255,255,255,0.2)] hover:-translate-y-0.5 active:translate-y-0 active:scale-[0.98] active:shadow-[inset_0_2px_4px_rgba(0,0,0,0.2)] transition-all duration-[300ms] ease-out focus-visible:outline-none focus-visible:shadow-[0_0_0_3px_rgba(99,91,255,0.3)] motion-reduce:transition-none motion-reduce:hover:translate-y-0 motion-reduce:active:scale-100">
  继续
</button>
```

密集表格里的次按钮用同一套上浮、按下和内高光，尺寸改为 `px-3 py-2`，颜色改为 `bg-white text-[#0a2540] border border-gray-200`。

### 卡片

```html
<section class="bg-white rounded-xl border border-gray-200 text-[#0a2540] shadow-[0_2px_4px_rgba(0,0,0,0.04),0_8px_16px_rgba(0,0,0,0.08)] hover:-translate-y-1 hover:shadow-[0_12px_30px_rgba(0,0,0,0.08)] transition-all duration-[400ms] ease-out motion-reduce:transition-none motion-reduce:hover:translate-y-0 p-6 md:p-8">
  <h3 class="font-semibold text-[#0a2540] text-xl md:text-2xl">标题</h3>
  <p class="text-gray-600 text-base">说明保持在大约 65 到 75 个字符宽。</p>
</section>
```

### 输入框

```html
<input class="bg-white border border-gray-300 rounded-lg text-[#0a2540] placeholder-gray-400 shadow-[0_1px_2px_rgba(0,0,0,0.05)] focus:outline-none focus:ring-2 focus:ring-[#635bff] focus:border-transparent transition-all duration-[300ms] ease-out" placeholder="卡号" />
```

### 布局

侧边栏是白底、宽 16rem，当前项用 `#635bff` 白字。顶栏是白底，包含导航、搜索、通知和账号。主区域铺 `#f6f9fc` 和 40px 网格。页面内不要再套一层导航。

内容区顺序：指标卡（大屏四列 / 中屏两列 / 小屏一列）→ 主面板占三分之二、辅助面板占三分之一 → 数据表单独一块。面板间距统一 `gap-6`。空状态和加载状态都要写出来。

### 层级与强调

一排同级元素全是白底，眼睛找不到起点。**每一组只给一个焦点**，按面积从大到小分三级：前两级用主色实心，第三级用主色描边。

| 级别 | 用在哪 | 写法 |
|---|---|---|
| 主卡 | 一排指标卡里的第一张，装本页要回答的核心数字（概览=余额、模型分析=调用总数、对账=费用） | 紫底白字，一屏最多一张 |
| 实心项 | 页内导航和分段切换的激活项；工具栏的主操作（数据页是「筛选」）；开关按钮「开」的状态 | 紫底白字 + 主按钮外阴影和内高光 |
| 描边项 | 次一级的重点，例如排行第 2–3 名 | 紫色描边，底色不变 |

- 排行名次：第 1 名实心、2–3 名描边、其余灰底。不用金银铜奖牌色（琥珀、橙色违反冲突第 7 条）。
- 页内导航比分段切换大一号（36px 对 32px），靠尺寸分层，不另换颜色。
- 紫底上的所有文字、图标都用白色，包括说明文字。`text-white/80` 对比度不到 4.5:1。
- **标签页**：激活态已经做进原语 `@/components/ui/tabs`（白底描边轨道 + 紫底白字激活项），全站自动生效。调用方只传尺寸和布局，**不要再写 `data-[state=active]:bg-*` / `shadow-*`**，否则会盖掉紫底、留下白字。触发器里自带颜色的徽标、状态点、灰字，用 `group-data-[state=active]/tab:*` 在激活时换成白色系（`text-primary-foreground`、`bg-white/20`）。等分网格的标签（`grid-cols-6` 这类）在窄屏放不下时改成两行（如 `grid h-auto grid-cols-3 lg:grid-cols-6`）：白字溢出紫底落到白色轨道上就看不见了。
- **单选卡片**（如充值金额）：选中项同样紫底白字，卡内的折扣、实付也换成白字。卡里有品牌 logo 的（如支付方式）例外，用紫色描边 + 光晕 + 对勾，logo 不压在紫底上。筛选芯片这类小元素（如模型广场厂商筛选）照样紫底白字，logo 外包一个固定尺寸的小方块，选中时方块垫白底、单色 logo 在白底上显示为主色（否则是白字压白底）；未选中时方块透明，选中前后尺寸不变。
- 现成写法在 `src/features/dashboard/components/overview/dash-emphasis.ts`：`dashHeroCard`、`dashSegTrack` + `dashSegItem`（自写分段切换）、`dashRankTone`、`dashPrimaryShadow`。控制台其他页面也从这里引，不要另抄一份。

### 控制台绝对不要

- 鲜艳的绿、红、黄铺底
- `rounded-3xl`、`rounded-full` 作为面板或按钮圆角
- `shadow-sm` 这类单层阴影代替多层阴影
- 漏掉网格背景 / 漏掉按钮内高光
- 卡片套卡片 / 在彩色背景上放灰色字
- 激活态用 `bg-background` 叠在 `bg-muted` 轨道上：两者只差一点灰，看不出选中了哪个
- 一屏两张以上主卡 / 一组按钮里两个以上实心主按钮（开关按钮「开」除外）

---

## 三、落地页：品牌蓝

对外页面用品牌蓝。**注意：仓库里的 `public/logo.png` 可能和后台配置的站点 logo 不是同一张，取色以实际上线的 logo 为准，不要照仓库文件。**

调性是「黑白灰骨架 + 一个蓝」，不是满屏蓝。蓝色只出现在强调位和行动点上。

### 色板

| 用途 | 亮色 | 暗色 |
|---|---|---|
| 强调色（标题高亮、状态点、图示、链接） | `#1b55e2` | `#78a0f0` |
| 墨色 / 主按钮 / 反色卡 / CTA / 代码面板底 / wordmark | `#0b1c47` | — |
| 暗色模式卡片表面 | — | `#111726` |
| 暗色模式次级按钮表面 | — | `#161c2b` |
| 页面底 | `#f4f6fa` | `#0a0c12` |
| 卡片底 | `#ffffff` | `#111726` |
| 次级文字 | `#5a6072` | `#9aa1b2` |
| 卡片描边 | `ring-black/6` | `ring-white/8` |

取用方式：颜色优先从 `src/features/home/components/home-links.tsx` 引 `homeAccent` / `homeMuted`。

目前有几处手写色值，改色时要一并替换：`features.tsx` 的两张 SVG 图示（用 `currentColor`，色值写在容器的 `text-[...]` 上）和卡片内的次级文字、`how-it-works.tsx` 的代码面板配色与复制按钮对勾图标、`console-preview.tsx` 的演示卡（余额主卡的反色底、装饰圆与余量条，其余指标卡的底色与数值色）。关于页 `src/features/about/components/` 也用这套色板（墨色、卡片底、胶囊底为手写色值），改色时连同首页一起替换。

**已验证的对比度**（对真实上色的祖先量，不是对透明层）：强调色 5.64:1、墨色 15.27:1、次级文字 5.79:1、主按钮 16.52:1，全部过 AA。改色后必须重量一遍。

### 字体与排版

- 正文 Manrope，标题 Archivo，定义在 `src/features/home/home-type.css` 的 `.home-type`
- 板块标题统一用 `<SectionHeading>`：`clamp(1.875rem,4.2vw,2.875rem)` / `leading-[1.08]` / `tracking-[-0.03em]` / `font-extrabold`。**行高必须收到 1.08**，1.25 会让两行标题散架
- Hero wordmark：`clamp(2.5rem,11vw,8.5rem)` / `leading-[0.94]` / `tracking-[-0.05em]`，颜色带 0.8 透明度（`text-[#0b1c47]/80`）。**透明度必须写在颜色 alpha 上**：入场动画以 `opacity: 1` 收尾，opacity 类会被覆盖

### 板块骨架

每个板块都按同一个节奏开场，不要各写各的：

```
<section className='px-6 py-16 md:py-24'>
  <div className='mx-auto max-w-6xl'>
    <Eyebrow>板块名</Eyebrow>              // 描边胶囊，h-7 rounded-full tracking-[0.08em]
    <SectionHeading>前半句 <span className={homeAccent}>后半句</span></SectionHeading>
    [可选] <SectionAside to='...'>次级入口 ↗</SectionAside>   // 右上角
    ...内容
```

标题的**后半句**套 `homeAccent` 上蓝，这是落地页强调色的主要出场方式。

### 按钮

胶囊形，`h-11 rounded-full px-5 text-sm font-medium`。直接用 `HomePrimaryLink` / `HomeSecondaryLink`，不要重写。

- 主：`bg-[#0b1c47] text-white` + 内高光 `inset_0_1px_0_rgba(255,255,255,0.18)`
- 次：`bg-white text-[#0b1c47] ring-1 ring-black/8` + 内高光 `inset_0_1px_0_rgba(255,255,255,0.7)`
- 反色卡内部的按钮要翻过来：主按钮白底深蓝字，次按钮透明 + `ring-white/25`

### 卡片

`rounded-xl` + `bg-white` + `ring-1 ring-black/6` + `p-7 md:p-8`，暗色 `dark:bg-[#111726] dark:ring-white/8`。

**一组卡片里留一张反色卡**（`bg-[#0b1c47]`，暗色翻成 `dark:bg-[#eef1f6]`），否则一排白卡会平。
宽卡跨两列时，文字走左列、配图走右列，不要把配图压在卡片最底下。

### 代码面板

底色 `#0b1c47`，语法着色如下（`how-it-works.tsx` 里那个轻量正则着色器，别为三段示例引整套高亮库）：

| 类别 | 色值 |
|---|---|
| 注释 | `#7c8698` |
| 字符串 | `#7fd1e8` |
| 关键字 | `#c9a6f2` |
| 数字 / 命令行开关 | `#e0a878` |
| 函数调用 | `#8ab4f8` |

冷色为主，只留数字和开关用琥珀做冷暖对比。

### 落地页绝对不要

- **把控制台那套搬过来**：40px 网格底、多层投影、`rounded-lg` 按钮，在落地页一律不用
- 中文加 `tracking-[0.28em]` 这类大字距（见第一章第 9 条）
- 手写颜色十六进制，绕开 `homeAccent` / `homeMuted`
- 满屏铺蓝。蓝只在强调位和行动点，骨架仍是黑白冷灰
- 浏览器外壳的红黄绿交通灯改成主题色——那是窗口控件语义，不是配色

---

## 交付前检查

**两套都要过：**

- 悬停只上浮不放大，按下 `active:scale-[0.98]`
- 焦点环可见，动效在 `prefers-reduced-motion` 下关闭
- 手机、平板、桌面没有横向溢出（能滚的元素要满足 `scrollWidth > clientWidth`，否则它是撑宽了父轨道而不是在滚）
- 中文没有被大字距拆散

**控制台额外：**

- 主色是 `#635bff`，卡片有多层阴影且悬停上浮
- 页面能看见 40px 网格
- 按钮有内高光，按下时外阴影换成内凹阴影
- 每组卡片、按钮、切换都有且只有一个焦点，激活项一眼能认出来（亮暗两个主题都看）

**落地页额外：**

- 颜色来自 `homeAccent` / `homeMuted`；手写十六进制只出现在上面列出的几处例外里
- 每个板块都是「Eyebrow 胶囊 → SectionHeading（后半句上蓝）」的开场
- 一组卡片里有反色卡，不是一排白卡
- 亮暗两个主题各看一遍，`dark:` 变体没有漏
