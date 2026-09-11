请完整执行“多仓库库存补货与履约分析”长任务，测试编号 ORKA-INVENTORY-20260911-F。直接完成，不需要反问。所有交付只写入你的用户工作区新目录 inventory_eval_20260911_f，不修改其他目录或项目代码；不要把任何已有评测的结果作为本次数据。

先建立至少8个可验收步骤的计划，边执行边更新。少量必要思考后开始调用工具，过程中简短报告实际进展；保留以下原始约束，不能在摘要后自行简化公式。先完成数据与业务计算，再进行有限调研和报告。全部运行脚本只允许 Python 3 标准库，不安装依赖。所有数字必须由脚本计算。

一、确定性原始数据与清洗
1. 主数据共有30天×3仓×12 SKU。d=0..29，日期为2026-09-01+d天的YYYY-MM-DD；w=0..2，warehouse=W1/W2/W3；s=0..11，sku=S01..S12。循环顺序为d、w、s。record_id=R{d:02d}{w}{s:02d}。CSV固定列：record_id,date,warehouse,sku,demand,receipts,unit_price_cents。
2. demand=1+((7*d+5*w+3*s)%19)。receipts在d%5==0时为35+3*w+s，否则为0。unit_price_cents=1999+137*s+31*w。全部数量、金额单位为整数，不使用浮点货币。
3. 正常1080条按以上顺序写入raw.csv，再按正常行0起始索引每隔37行复制一条（索引0、37、74……），附加到末尾。再附加4条坏数据：复制第一条正常记录并分别把record_id设为BAD-DATE/BAD-QTY/BAD-WH/BAD-PRICE，同时分别且仅修改date=2026-02-30、demand=-1、warehouse=W9、unit_price_cents=19.99。先验证字段，再按record_id保留首条有效记录去重；不要更换上述异常样本。输出clean.csv相同7列、rejected.csv原7列加reason（只包含坏数据）；quality.json记录raw_rows,clean_rows,duplicate_rows,rejected_rows。

二、严格按时间顺序模拟履约（不下真实采购单）
每个仓/SKU在d=0收货之前初始库存=20+5*w+2*s、初始欠单=0。每天先把当日receipts加入opening_stock，再优先满足opening_backlog，剩余库存再满足当日demand；所有未满足需求结转欠单，不丢单。daily.csv必须有：date,warehouse,sku,opening_stock,opening_backlog,receipts,demand,fulfilled_backlog,fulfilled_today,closing_stock,closing_backlog,shipped_units,revenue_cents。shipped_units=fulfilled_backlog+fulfilled_today；revenue_cents=shipped_units*当日unit_price_cents；下一日开盘库存/欠单等于上一日收盘值。仓之间不得调拨。保留1080行，排序同原数据。
metrics.json结构为overall对象、warehouses数组（3项含warehouse）、skus数组（36项含warehouse和sku）。每项都有 demand_units,shipped_units,fulfilled_today_units,fill_rate,ending_stock,ending_backlog,revenue_cents,backlog_days。fill_rate为当日及时满足总量/总需求，不把清偿旧欠单算入分子；ending_*仅求最后一天；backlog_days为该组每日仓/SKU closing_backlog>0的行数。
replenishment.csv共36行：warehouse,sku,lead_days,avg_daily_demand_7d,ending_stock,ending_backlog,suggested_order_units,p95_backlog。lead_days=2+s%3；最近7日指d=23..29（包含两端）；建议采购量=max(0,ceil((lead_days+2)*最近7日总需求/7-ending_stock+ending_backlog))，只计算建议不回灌模拟。p95_backlog将该仓/SKU的30个收盘欠单升序排列，取第ceil(0.95*30)项（1起始），禁止插值。

三、业务交付与有限官方调研
制作离线可打开的dashboard.html及charts/下至少3张SVG，分别体现30天欠单趋势、3仓当日满足率、36个仓/SKU采购建议。所有图表的数字要能追溯到上述JSON/CSV，HTML只引用本地资源，不能依赖CDN或网络。report.md至少包含业务发现、金额与欠单核对、排序明确的前5个采购优先项（建议量降序，平局warehouse再sku升序）、试运行风险及验收结果。
针对这套管道比较“CSV+JSON文件”和“SQLite”两种存储方案，比较4个维度：多文件/多表原子更新、并发写入、数据类型约束、崩溃恢复。每个方案每个维度都给出结论及直接官方证据链接，明确哪些是应用应实现的能力。只读docs.python.org和sqlite.org的正文文档，至少4个不同正文页面，两个域名至少各2页；目录/搜索页不算，不超过8次远程调研调用，网页不可达如实说明，不编造引用。sources.json逐条记录url,title,accessed_at,supported_claim；报告避免把SQLite普通表动态类型或STRICT表限制混为一谈。不需要比较付费搜索服务，也不需要扩展调研其他产品。

四、可复现性与真正的验收
脚本按职责拆分（可用generate.py、analyze.py、verify.py、render.py、package.py及小型共享模块），不得所有业务逻辑堆在单个巨型脚本。交付run_all.py，支持命令：python3 run_all.py --output /tmp/任意新目录；允许从任意当前目录运行，输出目录可不存在。该命令在断网且没有其他Python包的环境下可完整再现数据、报表、静态已保存调研材料、manifest和ZIP；不要运行时再抓网页。README.md写清一条完整可执行命令、目录结构、计算口径、退出码。
verify.py至少16项有实际断言的验证，覆盖去重、坏日期/负数量/仓/小数价格、库存与需求守恒、先旧后新分配、及时满足率、最后7天端点、向上取整、P95最近秩、金额整数、分组与总体一致性、HTML/SVG本地引用、文件及摘要完整性。任何断言失败必须非零退出，不能打印FAIL后仍声称全部通过；不能仅比对自己写出的固定常数。将执行结果记录verification.txt，并给出可复现的至少3个小型边界样例及预期结果，覆盖零库存、恰好满足旧欠单、跨日欠单。
最后生成manifest.json，格式为{"files":[{"path":"相对路径","size_bytes":整数,"sha256":"64位十六进制"}]}，列出交付目录内除manifest.json、delivery.zip和缓存外的全部交付文件（含脚本、README、sources、verification）。先把所有文件定稿后再算摘要，打包后不要改已入清单文件。delivery.zip必须包含manifest.json及清单列出的每个文件，使用相对路径，不能包含ZIP自身或缓存。实际检查解压后的字节、文件清单、CRC、摘要一致性；在独立新目录跑一遍完整命令再核验，不能只声称可运行。避免verification写入/manifest计算的循环依赖，请自己合理设计检查顺序。

最终回复简洁列出实际完成项、核心指标、验证结果和可下载ZIP/报告/看板链接。若受预算或工具限制，明确列出未完成或未通过项，不可将partial描述为全部完成。
