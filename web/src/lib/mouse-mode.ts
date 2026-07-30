import * as ls from '@/lib/localstorage.ts';
import { client } from '@/lib/websocket.ts';

// applyMouseMode records the choice and, for relative mode, restarts the
// websocket. The queue can still hold absolute reports for an endpoint the
// target is not reading, and those have to go before the new ones are sent.
export function applyMouseMode(mode: string, setMouseMode: (mode: string) => void) {
  setMouseMode(mode);
  ls.setMouseMode(mode);

  if (mode === 'relative') {
    client.close();
    setTimeout(() => {
      client.connect();
    }, 500);
  }
}
