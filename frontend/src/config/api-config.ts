export const API_CONFIG = {
  // Use environment variable from window.__ENV (runtime) or process.env (build/server)
  BASE_URL: (typeof window !== 'undefined' && window.__ENV?.NEXT_PUBLIC_API_BASE_URL) 
    ? window.__ENV.NEXT_PUBLIC_API_BASE_URL 
    : (process.env.NEXT_PUBLIC_API_BASE_URL || 'http://localhost:8091/api/v1'),
};
