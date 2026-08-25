# 第三方软件声明 / Third-Party Notices

本项目（GatewayHub，AGPL-3.0）使用了下列第三方开源软件的代码、设计或灵感。特此声明其许可证与版权归属。

---

## Uptime Kuma

- **用途**：健康检查「分段状态条 / 心跳条」（Heartbeat Bar）的视觉设计参考。
- **项目地址**：https://github.com/louislam/uptime-kuma
- **许可证**：MIT License
- **版权**：Copyright (c) 2021 Louis Lam

本项目仅参考其「心跳条」的视觉设计思路（横向由多段竖条组成、绿=正常/黄=待定/红=宕机的分段历史展示），**未复制其源代码**。相关实现位于 `web/src/components/UptimeBar.vue`。

MIT 许可全文如下：

```
MIT License

Copyright (c) 2021 Louis Lam

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```
