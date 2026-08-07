import { setupWorker } from 'msw/browser'
import { http, HttpResponse } from 'msw'

// Flip this to exercise each ION verdict against the desktop gate and badge:
// 'ok' | 'warn' | 'critical' | 'unavailable'.
const ION_VERDICT = 'ok'

export const handlers = [
  http.post('/api/auth/login', () => {
    return HttpResponse.json({
        code: 0,
        data: {
            token: 'mocked_token',
        },
    })
  }),
  http.get('/api/vm/ion', () => {
    return HttpResponse.json({
        code: 0,
        data: {
            total: 78643200,
            used: 70778880,
            free: 7864320,
            usageRate: 90,
            generations: 3,
            reserve: 8388608,
            measured: true,
            verdict: ION_VERDICT,
        },
    })
  }),
]
export const worker = setupWorker(...handlers)
