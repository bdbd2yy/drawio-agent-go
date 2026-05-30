import { API_CONFIG } from '@/config/api-config';
import { 
    Response, 
    AiAgentConfigResponseDTO, 
    CreateSessionResponseDTO, 
    ChatRequestDTO, 
    ChatResponseDTO 
} from '@/types/api';

const handleResponse = async <T>(response: globalThis.Response): Promise<Response<T>> => {
    if (!response.ok) {
        const errorText = await response.text();
        throw new Error(`HTTP error! status: ${response.status}, message: ${errorText}`);
    }
    const data = await response.json();
    if (data.code !== "0000") {
        throw new Error(data.info || 'Unknown API error');
    }
    return data;
};

export const agentApi = {
    /**
     * Query AI Agent Config List
     * Path: /api/v1/query_ai_agent_config_list
     */
    queryAiAgentConfigList: async (): Promise<Response<AiAgentConfigResponseDTO[]>> => {
        const response = await fetch(`${API_CONFIG.BASE_URL}/query_ai_agent_config_list`, {
            method: 'GET',
            headers: {
                'Content-Type': 'application/json',
            },
        });
        return handleResponse<AiAgentConfigResponseDTO[]>(response);
    },

    /**
     * Create Session
     * Path: /api/v1/create_session
     */
    createSession: async (agentId: string, userId: string): Promise<Response<CreateSessionResponseDTO>> => {
        const response = await fetch(`${API_CONFIG.BASE_URL}/create_session`, {
            method: 'POST',
            headers: {
                'Content-Type': 'application/json',
            },
            body: JSON.stringify({ agentId, userId }),
        });
        return handleResponse<CreateSessionResponseDTO>(response);
    },

    /**
     * Chat
     * Path: /api/v1/chat
     */
    chat: async (data: ChatRequestDTO): Promise<Response<ChatResponseDTO>> => {
        const response = await fetch(`${API_CONFIG.BASE_URL}/chat`, {
            method: 'POST',
            headers: {
                'Content-Type': 'application/json',
            },
            body: JSON.stringify(data),
        });
        return handleResponse<ChatResponseDTO>(response);
    }
};
