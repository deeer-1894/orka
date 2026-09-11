# 长任务固定输入

复测时仅替换测试编号和输出目录；每次使用新会话。验收标准见同目录的复测记录。

请完整执行一次“客服工单自动化方案与数据分析”长任务，测试编号 ORKA-LONG-20260911-A。请持续执行到交付完成，建立并维护不少于8个阶段的计划；遇到普通错误自行修复并重试，不要只写方案。全部新文件放在你的工作区新目录 long_eval_20260911_a，不修改该目录外已有文件，不发送消息或发布内容。

一、官方资料调研：比较 Eino、LangGraph、CrewAI、Microsoft AutoGen 四个智能体框架在持久化/恢复、人工确认、多智能体协作、工具调用、可观测性、部署六个维度的支持。联网搜索并实际读取至少12个不同的官方文档或官方仓库页面，每个框架至少3页。把每页URL、标题、读取日期、具体证据及适用限制写入 sources.json；最终比较表每个非空结论都引用对应来源，不确定的明确标注，不能编造实测或来源。保存 research.md，包含比较表、差异与针对客服工单项目的选型建议。

二、可复现的合成数据：只使用Python标准库生成3600条唯一工单，i取1到3600。ticket_id=T加i的六位零填充；day=2026-08-加(1+(i-1)%30)的两位零填充；channel依次按(i-1)%3映射email/chat/phone；priority按(i-1)%4映射low/medium/high/urgent；team按(i-1)%5映射A/B/C/D/E；first_response_min=(i*17)%240；resolution_min=30+(i*37)%2880；reopened=int(i%11==0)；csat在i%7==0时为空，否则1+i%5。生成完后，把i为100的倍数的36条记录各额外原样追加一次，再追加4条非法记录，ticket_id依次为BAD1到BAD4，其他值复制第一条有效记录，只分别将day改为2026-02-30、channel改为fax、first_response_min改为-1、csat改为9。保存 tickets_raw.csv，并用单独清洗步骤生成 tickets_clean.csv，去除重复ticket_id、隔离非法记录到 rejected.csv，写出数据质量统计 quality.json。请保留空csat，不把它填成0。

三、真实运行分析：按有效唯一工单计算总体、逐日、渠道、优先级和团队的数量、平均及P95首次响应时间、平均解决时间、重开率、满意度均值与有效样本数。P95使用nearest-rank定义。SLA阈值low=180、medium=120、high=60、urgent=15分钟，响应时间<=阈值算达标。输出结构化 metrics.json、daily.csv、channel.csv、priority.csv、team.csv；制作不少于3张无需联网即可查看的SVG图表和一个独立HTML分析页，所有数据从实际分析结果生成。

四、验证和交付：写可重复运行的 generate.py、analyze.py、verify.py（标准库即可），verify.py检查清洗数量、重复数、非法数、缺失满意度处理、分组总数、SLA边界和P95定义；实际执行并保存 verification.txt。写中文 report.md，将数据结论与框架选型结合，区分合成数据和真实调研、明确局限和下一步；写 README.md 给出一条从生成到验证的完整复现命令。最终为所有交付文件生成 manifest.json（相对路径、字节数、SHA256；排除manifest自身和压缩包），打包为 deliverables.zip。最后重新列出文件并检查必需产物，报告关键数字、验证结果、遇到的问题和可下载文件链接。若确实无法完成，明确指出未完成阶段，禁止把未执行的步骤标为完成。
