# 公开页面复刻设计验收

## Source Visual Truth

- 页面来源：`https://getgpt.pro/`，已通过浏览器采集首页、FAQ、教程、订单、产品、博客、帮助和政策页的路由、DOM、控件与跳转关系。
- 桌面源图：`/tmp/payment-kit-getgpt-home-same-viewport.png`
- 桌面实现图：`/tmp/payment-kit-home-final-desktop.png`
- 桌面合并对照图：`/tmp/payment-kit-home-final-desktop-comparison.png`
- 移动源图：`/tmp/payment-kit-getgpt-home-mobile.png`
- 移动实现图：`/tmp/payment-kit-home-final-mobile.png`
- 移动合并对照图：`/tmp/payment-kit-home-final-mobile-comparison.png`

## Capture Normalization

- 桌面：浏览器 CSS `innerWidth=1280`、`innerHeight=720`，内容区 `clientWidth=1265`，实现与源图均为 `1265 x 712` 像素。
- 移动：浏览器 CSS `innerWidth=390`、`innerHeight=844`，内容区 `clientWidth=375`，实现与源图均为 `375 x 812` 像素。
- 两组截图均由 Codex 浏览器渲染，使用同一浏览器会话和同一页面顶部状态；截图像素与 CSS 内容区按 1x 处理，未做缩放比较。

## Compared State

- 首页顶部、公告栏、导航、Hero、主 CTA 和滚动到充值流程入口。
- FAQ 默认分类、分类切换、问题展开和 `#aftersale` hash 进入售后分类。
- 教程目录锚点、本站流程示意卡、官方 Session 获取链接和三步操作说明。
- 产品页 CTA、订单页、博客标签筛选、博客文章详情、帮助搜索、移动端菜单。

## Comparison History

1. 首轮对照发现首页 Hero 右侧流程卡是实现额外增加的结构，和源站居中 Hero 不一致。已移除额外右栏，保留真实购买 CTA 与独立充值流程区，并补上“下滑查看充值流程”入口。
2. 首轮运行时发现旧的 `next dev` 进程继续引用 build 前的 CSS manifest，导致无样式和无客户端交互。已重启验收进程并固定 `API_INTERNAL_URL=http://127.0.0.1:28080`；商品卡片现在由 Go API 正常返回。
3. 发现 FAQ hash 只在首次挂载读取，客户端同页改变 hash 不会切换分类。已加入 `hashchange` 监听和分类定位。
4. 发现博客标签只是文本、不能进入标签页。已改为本地 Next Link，并让 `/blog/tag/[tag]` 按标签过滤文章。
5. 发现教程仍引用第三方视频和截图。已移除全部教程媒体引用，改为本站自绘流程示意卡，并在提交账号信息步骤增加官方 Session 地址。

## Fidelity Review

- 字体与排版：桌面、移动均保持独立页统一字体层级、标题/正文/辅助文本权重和换行；移动首屏文本无裁切。
- 间距与布局：首页改为居中 Hero；FAQ、教程、博客使用与源站一致的分区节奏和卡片层级；FAQ/教程移动端实测无横向溢出。
- 颜色与 tokens：布局、控件状态和对比关系已对照；品牌色、公告色和按钮色使用 KC ChatGPT 自有 teal/ink 主题，这是用户明确允许的差异。
- 图片与媒体：教程不再引用第三方视频或截图，流程示意由本站 HTML/CSS 组件渲染。
- 文案与业务：品牌和购买/兑换说明改为 KC 平台真实业务文案；购买、订单、CDK 和 Worker 入口仍通过 Go API，不在 TS 中伪造业务数据。
- 图标与控件：导航、CTA、折叠、标签、搜索、锚点、Session 外链和移动菜单均有真实可操作状态。

## Automated Evidence

- `npm run lint`：通过，0 errors；保留 7 条已有 warning（`<img>` 性能建议和后台未使用变量），不影响构建。
- `API_INTERNAL_URL=http://127.0.0.1:28080 npm run build`：通过，23 个 Next 页面生成成功。
- `node scripts/public-reference-pages-e2e.mjs`：通过，19 个公开路由/别名，覆盖内部导航无 document reload、FAQ hash/展开、教程锚点/媒体隔离/Session 外链、博客标签/文章、产品 CTA、帮助搜索和移动菜单；Go API 公开请求无 5xx，浏览器无 page error。
- 浏览器视觉检查：同视口桌面对照和移动端对照均已完成；FAQ 与教程移动端 body 宽度分别为 `375/375`，教程流程示意卡无横向溢出。

## Accepted Differences

- 颜色和品牌标识采用 KC ChatGPT 自有主题，不复制 GETGPT 品牌。
- 竞品的 Claude 外链及其未在本平台提供的服务没有伪造为本地可购买业务；当前本地产品页仍提供 Gemini、Grok、Codex 的信息与导航入口。
- 竞品的动态评价数据没有虚构写入，平台仅保留真实可验证的订单、CDK、兑换和帮助流程。

## Final Result

结构、公开路由、内部跳转和关键交互已通过验收；品牌/颜色/业务文案差异按约定保留。

final result: passed
