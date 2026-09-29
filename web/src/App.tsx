import { useCallback, useEffect, useMemo, useState } from 'react'
import type { FormEvent, ReactNode } from 'react'
import api, { type Conversation, type ConversationDetails, type Message, type Status } from './api'

type Page = 'dashboard' | 'conversations' | 'settings'
type IconKey = 'grid' | 'chat' | 'settings' | 'plus' | 'search' | 'arrow' | 'chevron' | 'check' | 'send' | 'spark' | 'clock' | 'inbox' | 'close' | 'refresh' | 'shield' | 'database' | 'plug'
function Icon({ name, size = 18 }: { name: IconKey; size?: number }) {
  const paths: Record<IconKey, ReactNode> = {
    grid: <><rect x="3" y="3" width="7" height="7" rx="1.5"/><rect x="14" y="3" width="7" height="7" rx="1.5"/><rect x="3" y="14" width="7" height="7" rx="1.5"/><rect x="14" y="14" width="7" height="7" rx="1.5"/></>,
    chat: <><path d="M20 11.5a7.5 7.5 0 0 1-7.5 7.5H5l-1.5 2v-6A7.5 7.5 0 1 1 20 11.5Z"/><path d="M8 11h8M8 14h5"/></>,
    settings: <><circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.7 1.7 0 0 0 .3 1.9l.1.1-2.4 2.4-.1-.1a1.7 1.7 0 0 0-2.9 1.2v.2h-3.4v-.2a1.7 1.7 0 0 0-2.9-1.2l-.1.1-2.4-2.4.1-.1a1.7 1.7 0 0 0-1.2-2.9h-.2v-3.4h.2a1.7 1.7 0 0 0 1.2-2.9l-.1-.1 2.4-2.4.1.1a1.7 1.7 0 0 0 2.9-1.2V6h3.4v.2a1.7 1.7 0 0 0 2.9 1.2l.1-.1 2.4 2.4-.1.1a1.7 1.7 0 0 0 1.2 2.9h.2v3.4h-.2a1.7 1.7 0 0 0-1.2.9Z"/></>,
    plus: <><path d="M12 5v14M5 12h14"/></>, search: <><circle cx="10.8" cy="10.8" r="6.8"/><path d="m16 16 4.5 4.5"/></>,
    arrow: <><path d="M5 12h14M13 6l6 6-6 6"/></>, chevron: <path d="m9 18 6-6-6-6"/>, check: <path d="m5 12 4 4L19 6"/>,
    send: <><path d="m21 3-7.5 18-3.8-7.7L2 9.5 21 3Z"/><path d="M9.7 13.3 21 3"/></>,
    spark: <><path d="m12 3 1.9 5.8L20 11l-6.1 2.2L12 19l-1.9-5.8L4 11l6.1-2.2L12 3Z"/><path d="m19 15 .8 2.2L22 18l-2.2.8L19 21l-.8-2.2L16 18l2.2-.8L19 15Z"/></>,
    clock: <><circle cx="12" cy="12" r="9"/><path d="M12 7v5l3 2"/></>, inbox: <><path d="M4 4h16l1 11h-5l-2 3h-4l-2-3H3L4 4Z"/><path d="M3 15h5l2 3h4l2-3h5"/></>,
    close: <><path d="m6 6 12 12M18 6 6 18"/></>, refresh: <><path d="M20 7v5h-5M4 17v-5h5"/><path d="M5.5 9a7 7 0 0 1 11.7-2L20 12M4 12l2.8 5a7 7 0 0 0 11.7-2"/></>,
    shield: <><path d="M12 22s8-4 8-11V5l-8-3-8 3v6c0 7 8 11 8 11Z"/><path d="m9 12 2 2 4-4"/></>, database: <><ellipse cx="12" cy="5" rx="8" ry="3"/><path d="M4 5v14c0 1.7 3.6 3 8 3s8-1.3 8-3V5M4 12c0 1.7 3.6 3 8 3s8-1.3 8-3"/></>,
    plug: <><path d="M12 22v-5M9 8V2M15 8V2M5 8h14v4a7 7 0 0 1-14 0V8Z"/></>,
  }
  return <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">{paths[name]}</svg>
}
const labels: Record<Status, string> = { bot: 'Automático', human: 'Atendimento humano', resolved: 'Resolvida' }
const title: Record<Page, string> = { dashboard: 'Visão geral', conversations: 'Conversas', settings: 'Configurações' }
function when(value: string, compact = false) { const d = new Date(value); if (Number.isNaN(d.getTime())) return '—'; const now = new Date(); return d.toDateString() === now.toDateString() ? new Intl.DateTimeFormat('pt-BR', { hour: '2-digit', minute: '2-digit' }).format(d) : new Intl.DateTimeFormat('pt-BR', compact ? { day: '2-digit', month: 'short' } : { day: '2-digit', month: 'short', hour: '2-digit', minute: '2-digit' }).format(d) }
function phone(value: string) { return value.startsWith('+') ? value : `+${value}` }
function initials(value: string) { return value.replace(/\D/g, '').slice(-2) || 'CL' }
function StatusBadge({ status }: { status: Status }) { return <span className={`status-badge status-${status}`}><i />{labels[status]}</span> }

export default function App() {
  const [path, setPath] = useState(location.pathname)
  const [health, setHealth] = useState<'checking' | 'online' | 'offline'>('checking')
  const [items, setItems] = useState<Conversation[]>([])
  const [loading, setLoading] = useState(false)
  const [listError, setListError] = useState('')
  const [filter, setFilter] = useState<Status | 'all'>('all')
  const [details, setDetails] = useState<ConversationDetails | null>(null)
  const [detailBusy, setDetailBusy] = useState(false)
  const [detailError, setDetailError] = useState('')
  const [query, setQuery] = useState('')
  const [busy, setBusy] = useState(false)
  const [actionError, setActionError] = useState('')
  const [draft, setDraft] = useState('')
  const [modal, setModal] = useState(false)
  const [newPhone, setNewPhone] = useState('')
  const [newText, setNewText] = useState('')
  const [modalError, setModalError] = useState('')
  const [modalBusy, setModalBusy] = useState(false)
  const [toast, setToast] = useState('')

  const page: Page = path.startsWith('/settings') ? 'settings' : path.startsWith('/conversations') ? 'conversations' : 'dashboard'
  const selected = useMemo(() => Number(path.match(/^\/conversations\/(\d+)/)?.[1]) || null, [path])
  const navigate = useCallback((to: string) => { history.pushState({}, '', to); setPath(to) }, [])
  useEffect(() => { const back = () => setPath(location.pathname); addEventListener('popstate', back); return () => removeEventListener('popstate', back) }, [])

  const loadList = useCallback(async () => {
    setLoading(true); setListError('')
    try { setItems(await api.list()) } catch (e) { setItems([]); setListError(e instanceof Error ? e.message : 'Não foi possível carregar as conversas.') }
    finally { setLoading(false) }
  }, [])
  const loadDetail = useCallback(async (id: number) => {
    setDetailBusy(true); setDetailError('')
    try { setDetails(await api.get(id)) } catch (e) { setDetails(null); setDetailError(e instanceof Error ? e.message : 'Não foi possível abrir a conversa.') }
    finally { setDetailBusy(false) }
  }, [])
  const checkHealth = useCallback(async () => { try { await api.health(); setHealth('online') } catch { setHealth('offline') } }, [])
  useEffect(() => { void loadList(); void checkHealth(); const t = setInterval(checkHealth, 15000); return () => clearInterval(t) }, [loadList, checkHealth])
  useEffect(() => { if (selected) void loadDetail(selected); else setDetails(null) }, [selected, loadDetail])
  useEffect(() => { if (page === 'conversations') void loadList() }, [page, loadList])
  useEffect(() => { if (!toast) return; const t = setTimeout(() => setToast(''), 3000); return () => clearTimeout(t) }, [toast])

  const counts = useMemo(() => ({ all: items.length, bot: items.filter(x => x.status === 'bot').length, human: items.filter(x => x.status === 'human').length, resolved: items.filter(x => x.status === 'resolved').length }), [items])
  const visible = useMemo(() => items.filter(x => (filter === 'all' || x.status === filter) && (!query || x.phone.toLowerCase().includes(query.toLowerCase()) || String(x.id).includes(query))), [items, filter, query])
  const refresh = () => { void loadList(); void checkHealth(); if (selected) void loadDetail(selected) }
  const showModal = () => { setModalError(''); setModal(true) }

  const takeOver = async () => {
    if (!details) return
    setBusy(true); setActionError('')
    try { await api.takeover(details.conversation.id); await loadDetail(details.conversation.id); await loadList(); setToast('Atendimento assumido pela equipe.') }
    catch (e) { setActionError(e instanceof Error ? e.message : 'Não foi possível assumir a conversa.') }
    finally { setBusy(false) }
  }
  const resolve = async () => {
    if (!details) return
    setBusy(true); setActionError('')
    try { await api.resolve(details.conversation.id); await loadDetail(details.conversation.id); await loadList(); setToast('Conversa resolvida.') }
    catch (e) { setActionError(e instanceof Error ? e.message : 'Não foi possível resolver a conversa.') }
    finally { setBusy(false) }
  }
  const sendMessage = async (event: FormEvent) => {
    event.preventDefault(); if (!details || !draft.trim()) return
    setBusy(true); setActionError('')
    try { await api.sendHuman(details.conversation.id, draft.trim()); setDraft(''); await loadDetail(details.conversation.id); await loadList(); setToast('Mensagem enviada.') }
    catch (e) { setActionError(e instanceof Error ? e.message : 'Não foi possível enviar a mensagem.') }
    finally { setBusy(false) }
  }
  const simulate = async (event: FormEvent) => {
    event.preventDefault(); setModalError('')
    if (!newPhone.trim() || !newText.trim()) { setModalError('Preencha o telefone e a mensagem.'); return }
    setModalBusy(true)
    try { const result = await api.simulate(newPhone.trim(), newText.trim(), `web-demo-${Date.now()}`); setModal(false); setNewPhone(''); setNewText(''); await loadList(); navigate(`/conversations/${result.conversation.id}`); setToast(result.duplicate ? 'Evento já processado.' : 'Mensagem recebida e processada.') }
    catch (e) { setModalError(e instanceof Error ? e.message : 'Não foi possível simular a mensagem.') }
    finally { setModalBusy(false) }
  }

  return <div className="shell">
    <aside className="sidebar">
      <button className="brand" onClick={() => navigate('/')}><span className="brand-mark"><Icon name="chat" size={20}/></span><span><b>atende</b><small>central de conversas</small></span></button>
      <div className="nav-caption">MENU PRINCIPAL</div>
      <nav><button className={`nav-link ${page === 'dashboard' ? 'active' : ''}`} onClick={() => navigate('/')}><Icon name="grid"/>Visão geral</button><button className={`nav-link ${page === 'conversations' ? 'active' : ''}`} onClick={() => navigate('/conversations')}><Icon name="chat"/>Conversas<span className="nav-count">{counts.all}</span></button></nav>
      <div className="nav-caption nav-caption-lower">PREFERÊNCIAS</div><nav><button className={`nav-link ${page === 'settings' ? 'active' : ''}`} onClick={() => navigate('/settings')}><Icon name="settings"/>Configurações</button></nav>
      <div className="sidebar-grow"/><div className="provider-card"><span className="provider-icon"><Icon name="spark" size={17}/></span><span><b>Demo ativa</b><small>Provider local</small></span><i/></div><div className="sidebar-user"><span className="mini-avatar">AT</span><span><b>Ambiente de demonstração</b><small>Projeto local</small></span></div>
    </aside>
    <main className="main-area">
      <header className="topbar"><div className="breadcrumbs"><span>Atende</span><Icon name="chevron" size={14}/><b>{title[page]}</b></div><div className="top-actions"><span className={`connection ${health}`}><i/>{health === 'online' ? 'Sistema conectado' : health === 'offline' ? 'API indisponível' : 'Conectando...'}</span><button className="icon-button" title="Atualizar" onClick={refresh}><Icon name="refresh"/></button><button className="button primary small" onClick={showModal}><Icon name="plus"/>Simular mensagem</button></div></header>
      {page === 'dashboard' && <section className="page">
        <div className="page-head"><div><span className="eyebrow">CENTRAL DE ATENDIMENTO</span><h1>Bom dia <span className="wave">✦</span></h1><p>Acompanhe o atendimento da sua equipe em um só lugar.</p></div><button className="button primary" onClick={showModal}><Icon name="plus"/>Nova mensagem</button></div>
        {listError && <InlineError text={listError} retry={() => void loadList()}/>}
        <div className="stats-grid"><Stat label="Total de conversas" value={loading || !!listError ? '—' : counts.all} note="Registradas na API" icon="chat" tone="violet" onClick={() => { setFilter('all'); navigate('/conversations') }}/><Stat label="Atendimento automático" value={loading || !!listError ? '—' : counts.bot} note="Com o provider local" icon="spark" tone="blue" onClick={() => { setFilter('bot'); navigate('/conversations') }}/><Stat label="Com a equipe" value={loading || !!listError ? '—' : counts.human} note="Aguardando atendimento" icon="inbox" tone="amber" onClick={() => { setFilter('human'); navigate('/conversations') }}/><Stat label="Resolvidas" value={loading || !!listError ? '—' : counts.resolved} note="Finalizadas" icon="check" tone="green" onClick={() => { setFilter('resolved'); navigate('/conversations') }}/></div>
        <div className="dashboard-columns"><section className="panel flow-card"><div className="panel-heading"><div><span className="eyebrow">COMO FUNCIONA</span><h2>Fluxo de atendimento</h2></div><span className="demo-label">VISÃO DA DEMO</span></div><p className="panel-subtitle">Da primeira mensagem até a resolução, com uma transição simples entre automação e equipe.</p><div className="flow-row"><Flow icon="chat" n="01" title="Mensagem recebida" caption="Entrada registrada"/><span className="flow-line"/><Flow icon="spark" n="02" title="Resposta automática" caption="Provider local"/><span className="flow-line"/><Flow icon="inbox" n="03" title="Equipe assume" caption="Atendimento humano"/><span className="flow-line"/><Flow icon="check" n="04" title="Resolvida" caption="Ciclo concluído"/></div></section>
          <section className="panel recent-card"><div className="panel-heading"><div><span className="eyebrow">ACOMPANHAMENTO</span><h2>Conversas recentes</h2></div><button className="link-button" onClick={() => navigate('/conversations')}>Ver todas <Icon name="arrow" size={15}/></button></div>{loading ? <Skeleton/> : items.length === 0 ? <Empty icon="inbox" title="Tudo tranquilo por aqui" text="Quando uma mensagem chegar, ela aparecerá nesta lista." action={<button className="link-button" onClick={showModal}>Simular mensagem <Icon name="arrow" size={15}/></button>}/> : items.slice(0, 5).map(item => <ConversationRow key={item.id} item={item} onClick={() => navigate(`/conversations/${item.id}`)}/>)}</section></div>
        <div className="page-foot">Dados carregados da API do projeto <span/> Atualizado agora</div>
      </section>}
      {page === 'conversations' && <section className="page conversations-page"><div className="page-head"><div><span className="eyebrow">ATENDIMENTO</span><h1>Conversas</h1><p>Visualize e acompanhe cada atendimento.</p></div><button className="button primary" onClick={showModal}><Icon name="plus"/>Simular mensagem</button></div><div className="workspace">
        <section className={`inbox-panel ${selected ? 'has-selection' : ''}`}><div className="inbox-head"><div><h2>Caixa de entrada</h2><small>{listError ? '—' : counts.all} {listError ? 'carregando' : counts.all === 1 ? 'conversa' : 'conversas'}</small></div><button className="icon-button" title="Atualizar lista" onClick={() => void loadList()}><Icon name="refresh"/></button></div><div className="filters">{([['all','Todas'],['bot','Automático'],['human','Humano'],['resolved','Resolvidas']] as const).map(([key, label])=><button key={key} className={filter === key ? 'filter active' : 'filter'} onClick={() => setFilter(key)}>{label}<span>{listError ? '—' : counts[key]}</span></button>)}</div><label className="search"><Icon name="search" size={17}/><input value={query} onChange={e => setQuery(e.target.value)} placeholder="Buscar telefone ou código"/><kbd>⌘ K</kbd></label>{listError && <InlineError text={listError} retry={() => void loadList()}/>}<div className="inbox-list">{loading ? <Skeleton/> : !listError && visible.length === 0 ? <Empty icon="search" title={query ? 'Nenhum resultado' : 'Nenhuma conversa ainda'} text={query ? 'Tente outro telefone ou código.' : 'Simule uma mensagem para iniciar um atendimento.'} action={!query ? <button className="link-button" onClick={showModal}>Simular mensagem <Icon name="arrow" size={15}/></button> : undefined}/> : visible.map(item=><ConversationRow key={item.id} item={item} selected={selected === item.id} onClick={() => navigate(`/conversations/${item.id}`)}/>)}</div></section>
        <section className={`chat-panel ${selected ? 'show-chat' : ''}`}>{!selected && <div className="chat-empty"><span className="empty-illustration"><Icon name="chat" size={27}/></span><h2>Seu atendimento começa aqui</h2><p>Selecione uma conversa para ver o histórico e gerenciar o atendimento.</p><button className="button outline" onClick={showModal}><Icon name="plus"/>Simular primeira mensagem</button></div>}{selected && detailBusy && <div className="chat-loading"><span className="spinner"/>Carregando conversa...</div>}{selected && !detailBusy && detailError && <div className="chat-empty"><span className="empty-illustration"><Icon name="inbox" size={25}/></span><h2>Não foi possível abrir</h2><p>{detailError}</p><button className="button outline" onClick={() => void loadDetail(selected)}>Tentar novamente</button></div>}{selected && !detailBusy && !detailError && details && <Chat details={details} busy={busy} error={actionError} draft={draft} setDraft={setDraft} takeOver={takeOver} resolve={resolve} send={sendMessage} goBack={() => navigate('/conversations')}/>}</section>
      </div></section>}
      {page === 'settings' && <Settings health={health} apiURL={import.meta.env.VITE_API_URL || 'http://localhost:8080'} check={() => void checkHealth()}/>}
    </main>
    {toast && <div className="toast"><span><Icon name="check" size={15}/></span>{toast}</div>}
    {modal && <div className="modal-layer" onMouseDown={e => { if (e.target === e.currentTarget) setModal(false) }}><section className="modal" role="dialog" aria-modal="true" aria-labelledby="modal-title"><div className="modal-top"><span className="modal-symbol"><Icon name="chat"/></span><button className="icon-button" onClick={() => setModal(false)} aria-label="Fechar"><Icon name="close"/></button></div><span className="eyebrow">NOVA ENTRADA</span><h2 id="modal-title">Simular mensagem recebida</h2><p>Crie uma conversa de demonstração com um contato fictício. A resposta usa o provider local.</p><form onSubmit={simulate}><label>Telefone do contato<input autoFocus value={newPhone} onChange={e => setNewPhone(e.target.value)} placeholder="Ex.: 5511990000001"/></label><label>Mensagem<textarea rows={3} value={newText} onChange={e => setNewText(e.target.value)} placeholder="Escreva a mensagem do cliente..."/></label>{modalError && <div className="form-error">{modalError}</div>}<div className="modal-actions"><span><Icon name="shield" size={15}/>Use somente dados fictícios</span><button className="button primary" disabled={modalBusy}>{modalBusy ? <><i className="button-spinner"/>Enviando</> : <><Icon name="send" size={16}/>Receber mensagem</>}</button></div></form></section></div>}
  </div>
}

function Stat({ label, value, note, icon, tone, onClick }: { label:string; value:number|string; note:string; icon:IconKey; tone:string; onClick:()=>void }) { return <button className="stat-card" onClick={onClick}><span className={`stat-icon ${tone}`}><Icon name={icon}/></span><span className="stat-label">{label}</span><b className="stat-value">{value}</b><span className="stat-note">{note}<Icon name="arrow" size={14}/></span></button> }
function Flow({ icon, n, title, caption }: { icon:IconKey; n:string; title:string; caption:string }) { return <div className="flow-step"><span className="flow-number">{n}</span><span className="flow-icon"><Icon name={icon}/></span><b>{title}</b><small>{caption}</small></div> }
function Empty({ icon, title, text, action }: { icon:IconKey; title:string; text:string; action?:ReactNode }) { return <div className="empty-state"><span className="empty-icon"><Icon name={icon}/></span><b>{title}</b><p>{text}</p>{action}</div> }
function Skeleton() { return <div className="skeleton"><i/><i/><i/></div> }
function InlineError({ text, retry }: { text:string; retry:()=>void }) { return <div className="inline-error"><span>{text}</span><button onClick={retry}>Tentar novamente</button></div> }
function ConversationRow({ item, selected=false, onClick }: { item:Conversation; selected?:boolean; onClick:()=>void }) { return <button className={`conversation-row ${selected?'selected':''}`} onClick={onClick}><span className={`person-avatar avatar-${item.status}`}>{initials(item.phone)}</span><span className="row-content"><span className="row-line"><b>{phone(item.phone)}</b><time>{when(item.updated_at,true)}</time></span><span className="row-meta"><span>Conversa #{item.id}</span><StatusBadge status={item.status}/></span></span><Icon name="chevron" size={15}/></button> }

function Chat({ details, busy, error, draft, setDraft, takeOver, resolve, send, goBack }: { details:ConversationDetails; busy:boolean; error:string; draft:string; setDraft:(s:string)=>void; takeOver:()=>void; resolve:()=>void; send:(e:FormEvent)=>void; goBack:()=>void }) {
  const c = details.conversation
  const messages = [...details.messages].sort((a,b)=>new Date(a.created_at).getTime()-new Date(b.created_at).getTime() || a.id-b.id)
  return <div className="chat-detail"><header className="chat-header"><button className="back-button icon-button" onClick={goBack} aria-label="Voltar"><Icon name="chevron"/></button><span className={`person-avatar large avatar-${c.status}`}>{initials(c.phone)}</span><div className="chat-person"><h2>{phone(c.phone)}</h2><div><span>Conversa #{c.id}</span><span className="divider"/><StatusBadge status={c.status}/></div></div><span className="chat-time"><Icon name="clock" size={15}/>{when(c.updated_at)}</span></header><div className="message-scroll">{messages.length===0 && <Empty icon="chat" title="Sem mensagens" text="Esta conversa ainda não possui mensagens."/>}{messages.map(m=><Bubble key={m.id} message={m}/>)}</div><div className="chat-footer">{error && <div className="action-error">{error}</div>}{c.status==='bot' && <div className="bot-actions"><div className="auto-hint"><span><Icon name="spark" size={16}/></span><div><b>Atendimento automático ativo</b><small>Acompanhado pelo provider local.</small></div></div><div className="button-row"><button className="button outline" disabled={busy} onClick={resolve}>Resolver</button><button className="button primary" disabled={busy} onClick={takeOver}>{busy ? <><i className="button-spinner"/>Aguarde...</> : <><Icon name="inbox" size={16}/>Assumir atendimento</>}</button></div></div>}{c.status==='human' && <><div className="human-banner"><i/>Atendimento assumido pela equipe</div><form className="composer" onSubmit={send}><textarea rows={1} placeholder="Digite uma mensagem..." value={draft} disabled={busy} onChange={e=>setDraft(e.target.value)} onKeyDown={e=>{if(e.key==='Enter'&&!e.shiftKey){e.preventDefault();if(draft.trim()&&!busy)e.currentTarget.form?.requestSubmit()}}}/><div className="composer-foot"><span>Enter para enviar · Shift + Enter para nova linha</span><div className="button-row"><button type="button" className="button outline" disabled={busy} onClick={resolve}>Resolver conversa</button><button className="button primary" disabled={busy||!draft.trim()}>{busy?<><i className="button-spinner"/>Enviando</>:<><Icon name="send" size={16}/>Enviar</>}</button></div></div></form></>}{c.status==='resolved' && <div className="resolved-banner"><span><Icon name="check" size={17}/></span><div><b>Atendimento resolvido</b><small>Esta conversa está em modo somente leitura.</small></div></div>}</div></div>
}
function Bubble({ message:m }: { message:Message }) { const incoming=m.direction==='inbound'; const human=m.sender==='human'; return <div className={`message-line ${incoming?'incoming':'outgoing'} ${human?'human-message':''}`}><div className="bubble-wrap">{!incoming&&<span className="sender-label">{human&&<i>AT</i>}{human?'Atendente':'Atende · automático'}</span>}<div className="bubble">{m.content}</div><time>{when(m.created_at)}</time></div></div> }
function Settings({ health, apiURL, check }: { health:'checking'|'online'|'offline'; apiURL:string; check:()=>void }) { return <section className="page settings-page"><div className="page-head"><div><span className="eyebrow">PREFERÊNCIAS</span><h1>Configurações</h1><p>Informações do ambiente desta demonstração.</p></div></div><div className="settings-layout"><div className="settings-main"><section className="panel connection-panel"><div className="setting-heading"><span><Icon name="plug"/></span><div><h2>Conexões</h2><p>Serviços que compõem o ambiente local.</p></div></div><Setting label="API" detail={apiURL} badge={health==='online'?'Conectada':health==='offline'?'Indisponível':'Verificando'} good={health==='online'}/><Setting label="Provider de IA" detail="Local · resposta determinística" badge="Ativo" good/><Setting label="Banco de dados" detail="PostgreSQL" badge="Configurado" good/><Setting label="Ambiente" detail="Demo local" badge="Demonstração"/></section><section className="panel about-panel"><span className="about-kicker"><Icon name="spark" size={16}/>SOBRE ESTE MVP</span><h2>Atendimento simples, com espaço para crescer.</h2><p>Automação de atendimento com API REST, PostgreSQL e provider de IA desacoplado. Esta interface demonstra a passagem entre resposta automática e equipe humana usando os recursos atuais do backend.</p><div className="tech-tags"><span>Go</span><span>REST API</span><span>PostgreSQL</span><span>Provider local</span></div></section></div><aside className="settings-aside"><section className="panel health-card"><div className="health-card-top"><span className={`health-symbol ${health}`}><Icon name={health==='online'?'check':health==='offline'?'close':'clock'}/></span><StatusBadge status={health==='online'?'human':health==='offline'?'resolved':'bot'}/></div><h3>{health==='online'?'Tudo funcionando':health==='offline'?'API sem conexão':'Verificando conexão'}</h3><p>{health==='online'?'A aplicação está alcançando o backend local.':health==='offline'?'Inicie o backend para habilitar as conversas.':'Aguardando resposta do backend.'}</p><button className="button outline full" onClick={check}><Icon name="refresh" size={16}/>Verificar novamente</button></section><div className="security-note"><Icon name="shield" size={17}/><p><b>Ambiente de demonstração</b>Este projeto não tem autenticação e deve permanecer local.</p></div></aside></div></section> }
function Setting({ label, detail, badge, good=false }: { label:string; detail:string; badge:string; good?:boolean }) { return <div className="setting-row"><span><b>{label}</b><small>{detail}</small></span><span className={`setting-badge ${good?'good':''}`}><i/>{badge}</span></div> }
