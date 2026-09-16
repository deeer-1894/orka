import { useRef, useState, type CSSProperties } from 'react';
import { ActionChip } from './ActionChip';
import { Icon, type IconName } from './Icon';

// Content stays separate from the carousel's interaction and layout.
const EXAMPLES: { category: string; title: string; description: string; icon: IconName; tone: string; steps: string[]; prompt: string }[] = [
  { category: '深度调研', title: '把问题，变成有依据的结论', description: '交叉核对真实来源，整理清晰的对比报告。', icon: 'search', tone: 'research', steps: ['联网搜索', '核对来源', '交付报告'], prompt: '调研 LangGraph、Eino、AutoGen 的设计差异。从官方文档交叉验证，比较长任务执行、上下文管理和中断恢复，整理带来源链接的对比报告并保存为 report.md。无法核实的内容明确标注。' },
  { category: '数据分析', title: '从一张表，读懂变化', description: '清洗数据、分析趋势，让发现变成图表。', icon: 'chart', tone: 'data', steps: ['读取数据', '分析趋势', '生成图表'], prompt: '分析我提供的数据文件：先检查字段、缺失值和异常记录，再分析主要趋势，用图表展示发现并生成分析报告。只使用实际数据，没有文件时先告诉我需要提供什么。' },
  { category: '浏览器', title: '打开网页，带回关键信息', description: '浏览真实站点，筛选信息并保留来源。', icon: 'globe', tone: 'browser', steps: ['打开网页', '筛选信息', '整理要点'], prompt: '打开 Hacker News，读取首页前 10 条内容，记录标题和原始链接，筛选与 AI 相关的内容并阅读原文，整理中文摘要。区分网页事实与分析，无法访问的内容明确说明。' },
  { category: '办公文档', title: '从一个想法，到一份演示', description: '梳理结构、组织内容，交付可下载的文稿。', icon: 'deck', tone: 'office', steps: ['梳理提纲', '设计页面', '导出演示'], prompt: '制作一份 6 页演示文稿，向非技术同事介绍 AI Agent：包含定义、核心架构、典型应用和局限。以清晰图解和简短文字呈现，导出 PPTX，检查文件后提供下载入口。' },
  { category: '编程项目', title: '让想法，成为能运行的作品', description: '拆解需求、编写代码，通过测试再交付。', icon: 'code', tone: 'code', steps: ['设计结构', '编写实现', '运行验证'], prompt: '开发一个只使用 Python 标准库的 CSV 数据检查小项目，支持检查缺失值、重复行和列数不一致，导出 JSON 与 HTML 报告。拆分解析、检查和导出模块，编写并实际运行测试，提供 README 和 ZIP。若代码执行能力未启用，先明确提示，不要把手写输出冒充运行结果。' },
];
const SCATTER = [{ x: -150, y: 36, r: -8 }, { x: 128, y: -64, r: 6 }, { x: -86, y: -88, r: -5 }, { x: 162, y: 50, r: 10 }, { x: -182, y: -14, r: -12 }];

export function HomeWelcome({ onPick, bottomInset = 0 }: { onPick: (prompt: string) => void; bottomInset?: number }) {
  const [active, setActive] = useState(0);
  const [hovered, setHovered] = useState<number | null>(null);
  const gesture = useRef<number | null>(null);
  const swiped = useRef(false);
  const choose = (index: number) => setActive((index + EXAMPLES.length) % EXAMPLES.length);
  return <section className="home-welcome" aria-label="Orka 首页" style={{ paddingBottom: Math.max(bottomInset + 24, 120) }}>
    <div className="home-intro">
      <div className="home-badge"><Icon name="sparkle" size={14} /> AI 自动化执行平台</div>
      <div className="home-logo" aria-hidden="true">O</div>
      <h1>交给 Orka 去执行</h1>
      <p>描述一个目标，它会拆解步骤、联网调研、调用工具，<br className="hidden sm:block" />把完整的成果交给你。</p>
    </div>
    <div className="home-stage" aria-label="任务示例" role="region" aria-roledescription="轮播"
      onClick={event => { if (!swiped.current && event.target === event.currentTarget) choose(active + 1); }}
      onPointerDown={event => { swiped.current = false; if (event.pointerType !== 'mouse') gesture.current = event.clientX; }}
      onPointerUp={event => { if (gesture.current !== null) { const dx = event.clientX - gesture.current; if (Math.abs(dx) > 35) { swiped.current = true; choose(active + (dx < 0 ? 1 : -1)); } gesture.current = null; } }}
      onPointerCancel={() => { gesture.current = null; }}
      onKeyDown={event => { if (event.key === 'ArrowRight' || event.key === 'ArrowLeft') { event.preventDefault(); const next = (active + (event.key === 'ArrowRight' ? 1 : EXAMPLES.length - 1)) % EXAMPLES.length; choose(next); event.currentTarget.querySelectorAll<HTMLButtonElement>('.home-card')[next]?.focus(); } }}>
      {EXAMPLES.map((example, index) => {
        const distance = Math.abs(index - active), near = Math.min(distance, 1), far = Math.max(distance - 1, 0);
        const point = SCATTER[index], highlighted = hovered === index && distance > 0;
        const style = {
          '--card-x': `${point.x * (near + far * .55)}px`, '--card-y': `${point.y * (near + far * .55) - far * 14}px`,
          '--card-rotation': `${point.r * Math.min(near + far * .4, 1.4)}deg`, '--card-scale': Math.min(1, Math.max(.4, 1 - distance * .17) + (highlighted ? .05 : 0)),
          '--card-blur': `${Math.min(distance * 3.4, 10) * (highlighted ? .45 : 1)}px`, '--card-opacity': Math.min(1, Math.max(.1, 1 - distance * .28) + (highlighted ? .22 : 0)), zIndex: 10 - distance,
        } as CSSProperties;
        return <button type="button" key={example.category} className="home-card" data-tone={example.tone} style={style} aria-pressed={index === active}
          aria-label={`${example.category}：${example.title}`} onClick={() => { if (!swiped.current) choose(index); }}
          onMouseEnter={() => setHovered(index)} onMouseLeave={() => setHovered(null)} onFocus={() => choose(index)}>
          <span className="home-card-heading"><span className="home-card-icon"><Icon name={example.icon} size={19} /></span><span>{example.category}</span></span>
          <span className="home-card-title">{example.title}</span>
          <span className="home-card-description">{example.description}</span>
          <span className="home-card-flow">{example.steps.map((step, i) => <span key={step}>{i > 0 && <span aria-hidden="true" className="home-flow-arrow">→</span>}<span className="home-step">{step}</span></span>)}</span>
        </button>;
      })}
    </div>
    <div className="home-controls">
      <div className="home-dots" role="group" aria-label="切换任务示例">{EXAMPLES.map((example, i) => <button type="button" key={example.category} aria-label={`查看${example.category}示例`} aria-pressed={i === active} onClick={() => choose(i)}><span /></button>)}</div>
      <p className="home-hint">点卡片或空白处切换，也可以左右滑动</p>
      <ActionChip icon="plus" onClick={() => onPick(EXAMPLES[active].prompt)}>使用这个示例</ActionChip>
    </div>
  </section>;
}
