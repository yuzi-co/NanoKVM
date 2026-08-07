// The shape of GET /api/vm/ion. Two unrelated surfaces read it - the quiet row
// in Settings and the desktop gate - so it lives in its own module rather than
// inside either component.
export type IonStatus = {
  total: number;
  used: number;
  free: number;
  usageRate: number;
  generations: number;
  reserve: number;
  measured: boolean;
  verdict: 'ok' | 'warn' | 'critical' | 'unavailable';
};
