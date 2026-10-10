import { apiRequest } from './client';

export interface ChatConfig {
  configured: boolean;
  base_url: string;
  has_key: boolean;
  key_hint: string;
  model: string;
  models?: string[];
  reasoning_effort: string;
  efforts?: string[];
  temperature: number;
  system_prompt: string;
}

export async function fetchChatConfig(): Promise<ChatConfig> {
  return apiRequest<ChatConfig>('/api/chat/config');
}

export async function updateChatConfig(payload: Partial<ChatConfig> & { api_key?: string }): Promise<ChatConfig> {
  return apiRequest<ChatConfig>('/api/chat/config', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  });
}

export async function sendChatMessage(prompt: string, history: any[] = []): Promise<{
  reply: string;
  reasoning?: string;
}> {
  return apiRequest('/api/chat', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ prompt, history }),
  });
}
