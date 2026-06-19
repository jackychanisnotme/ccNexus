# AINexus Distribution Site

这是第一阶段分发验证独立站，使用 Vue + Vite 构建为静态站点。

## 开发

```bash
npm install
npm run dev
npm run preview
```

## 验证

```bash
npm test
npm run build
```

## 配置入口

所有下载、支付、支持和教程链接集中在 `src/site.config.ts`。

第一阶段默认使用占位链接：

- 付费产品按钮显示为“联系获取”。
- 下载链接默认指向 GitHub Release 或本地文档。
- SHA256 和镜像 digest 在真实发布后替换。

替换真实链接时只更新配置文件，不需要改页面组件。

## 中英文与 IP 语言判定

站点支持 `zh-CN` 和 `en-US`。

- 首次访问会请求 `src/site.config.ts` 中的 `localization.geoEndpoint`。
- 当返回国家/地区码为 `CN`、`HK`、`MO`、`TW` 时默认中文，其它地区默认英文。
- IP 服务失败时回退到浏览器语言。
- 用户点击右上角语言按钮后会记住手动选择。

如需改为 Cloudflare Worker、自有 Geo API 或其它 IP 服务，只需要替换：

```ts
localization: {
  geoEndpoint: 'https://your-geo-endpoint.example/json',
  countryCodeField: 'country_code'
}
```
