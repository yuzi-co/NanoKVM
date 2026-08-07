import { useEffect, useState } from 'react';
import { Tooltip } from 'antd';
import { CircleHelpIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import * as api from '@/api/vm.ts';

import type { IonStatus } from '../../../ion-status/model';

function megabytes(bytes: number) {
  return `${Math.round(bytes / (1024 * 1024))} MB`;
}

export const VideoMemory = () => {
  const { t } = useTranslation();
  const [status, setStatus] = useState<IonStatus>();

  useEffect(() => {
    api
      .getIon()
      .then((rsp: any) => {
        if (rsp.code !== 0) {
          console.log(rsp.msg);
          return;
        }
        setStatus(rsp.data);
      })
      .catch((err) => console.log(err));
  }, []);

  // A board that cannot report its carveout shows nothing at all. A diagnostic
  // is not worth a row that says it does not work.
  if (!status || status.verdict === 'unavailable') {
    return null;
  }

  const toneClass =
    status.verdict === 'critical'
      ? 'text-red-400'
      : status.verdict === 'warn'
        ? 'text-amber-400'
        : '';

  return (
    <div className="flex w-full items-start justify-between">
      <div className="flex items-center space-x-2">
        <span>{t('settings.about.videoMemory')}</span>
        <Tooltip
          title={t('settings.about.videoMemoryTip')}
          className="cursor-pointer text-neutral-500"
          placement="right"
        >
          <CircleHelpIcon size={15} />
        </Tooltip>
      </div>

      <div className="flex flex-col items-end space-y-1">
        <span className={toneClass}>
          {megabytes(status.used)} / {megabytes(status.total)} ({status.usageRate}%)
        </span>

        {status.generations > 1 && (
          <span className="text-xs text-neutral-500">
            {t('settings.about.videoMemoryGenerations', { count: status.generations - 1 })}{' '}
            {t('settings.about.videoMemoryReboot')}
          </span>
        )}
      </div>
    </div>
  );
};
