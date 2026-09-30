export type Status = 'bot' | 'human' | 'resolved'
export interface Conversation { id: number; contact_id: number; phone: string; status: Status; created_at: string; updated_at: string }
export interface Message { id: number; conversation_id: number; direction: 'inbound' | 'outbound'; content: string; sender: 'contact' | 'ai' | 'human'; external_id?: string; reply_to_id?: number; created_at: string }
export interface ConversationDetails { conversation: Conversation; messages: Message[] }
export interface SimulationResult { conversation: Conversation; incoming_message: Message; reply?: Message; duplicate?: boolean }

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  let response: Response
  try {
    response = await fetch(path, { ...init, headers: { ...(init?.body ? { 'Content-Type': 'application/json' } : {}), ...init?.headers } })
  } catch {
    throw new Error('Não foi possível conectar à API. Verifique se o backend está iniciado.')
  }
  if (!response.ok) {
    const detail = (await response.text()).trim()
    const friendly: Record<number, string> = {
      400: 'Confira os dados informados e tente novamente.', 404: 'Esta conversa não foi encontrada.',
      409: 'Esta conversa já foi resolvida.', 502: 'O serviço de resposta está indisponível no momento.',
      503: 'O serviço está ocupado. Tente novamente em instantes.',
    }
    throw new Error(friendly[response.status] ?? (detail && response.status < 500 ? detail : 'Ocorreu um erro. Tente novamente.'))
  }
  return response.json() as Promise<T>
}

const api = {
  health: () => request<{ status: string }>('/healthz'),
  list: (status?: Status | 'all') => request<Conversation[]>(`/api/conversations${status && status !== 'all' ? `?status=${status}` : ''}`),
  get: (id: number) => request<ConversationDetails>(`/api/conversations/${id}`),
  takeover: (id: number) => request<Conversation>(`/api/conversations/${id}/takeover`, { method: 'POST' }),
  resolve: (id: number) => request<Conversation>(`/api/conversations/${id}/resolve`, { method: 'POST' }),
  sendHuman: (id: number, content: string) => request<Message>(`/api/conversations/${id}/messages`, { method: 'POST', body: JSON.stringify({ content }) }),
  simulate: (phone: string, content: string, externalId: string) => request<SimulationResult>('/api/simulated/whatsapp/messages', { method: 'POST', body: JSON.stringify({ phone, content, external_id: externalId }) }),
}
export default api
