import { AlertCircle, AlertTriangle } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import * as api from '@/api/vm.ts';

import type { IonStatus } from './model';

export function IonWarningBadge({ status }: { status: IonStatus | null }) {
  const { t } = useTranslation();

  if (status?.verdict !== 'warn') {
    return null;
  }

  return (
    <div className="pointer-events-none absolute left-1/2 top-4 z-10 flex max-w-[calc(100%-2rem)] -translate-x-1/2 items-center gap-2.5 rounded-lg border border-amber-500/50 bg-neutral-900/90 px-4 py-2.5 text-sm font-medium text-amber-400 shadow-xl shadow-amber-900/20 backdrop-blur-md">
      <AlertTriangle className="h-5 w-5 shrink-0" />
      <span className="min-w-0">{t('ion.warn')}</span>
    </div>
  );
}

export function IonCriticalGate({ onContinue }: { onContinue: () => void }) {
  const { t } = useTranslation();

  function reboot() {
    api.reboot().catch((err: unknown) => console.log(err));
  }

  return (
    <div className="absolute inset-0 z-10 flex items-center justify-center bg-black/80 p-6">
      <div className="flex max-w-md flex-col gap-4 rounded-lg border border-red-500/50 bg-neutral-900/95 p-6 shadow-xl">
        <div className="flex items-center gap-2.5 text-red-400">
          <AlertCircle className="h-5 w-5 shrink-0" />
          <span className="font-medium">{t('ion.criticalTitle')}</span>
        </div>

        <p className="text-sm text-neutral-300">{t('ion.criticalBody')}</p>

        <div className="flex justify-end gap-3">
          <button
            className="rounded px-3 py-1.5 text-sm text-neutral-400 hover:text-neutral-200"
            onClick={onContinue}
          >
            {t('ion.criticalContinue')}
          </button>
          <button
            className="rounded bg-red-600 px-3 py-1.5 text-sm text-white hover:bg-red-500"
            onClick={reboot}
          >
            {t('ion.criticalReboot')}
          </button>
        </div>
      </div>
    </div>
  );
}
