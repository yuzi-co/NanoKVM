import { useEffect, useState } from 'react';

import * as api from '@/api/vm.ts';

import type { IonStatus } from './model';

// The check must land before the stream starts. At the critical verdict, opening
// the stream is the event that kills the server, so a warning that arrives after
// the crash has no value.
//
// The stream is held while the request is in flight, and released whether the
// request succeeds or fails. A broken endpoint must not cost the operator their
// video.
export function useIonStatus() {
  const [status, setStatus] = useState<IonStatus | null>(null);
  const [loading, setLoading] = useState(true);
  const [overridden, setOverridden] = useState(false);

  useEffect(() => {
    let live = true;

    api
      .getIon()
      .then((rsp: any) => {
        if (!live) return;
        if (rsp.code === 0) {
          setStatus(rsp.data);
        }
      })
      .catch((err) => console.log(err))
      .finally(() => {
        if (live) setLoading(false);
      });

    return () => {
      live = false;
    };
  }, []);

  const holdStream = loading || (status?.verdict === 'critical' && !overridden);

  return {
    status,
    holdStream,
    continueAnyway: () => setOverridden(true)
  };
}
