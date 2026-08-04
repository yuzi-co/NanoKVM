import { Tooltip } from 'antd';
import { useAtom } from 'jotai';
import { Volume2Icon, VolumeXIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import { audioMutedAtom } from '@/jotai/audio.ts';

export const Speaker = () => {
  const { t } = useTranslation();
  const [isMuted, setIsMuted] = useAtom(audioMutedAtom);

  return (
    <Tooltip
      title={isMuted ? t('speaker.unmute') : t('speaker.mute')}
      placement="bottom"
      mouseEnterDelay={0.6}
    >
      <div
        className="flex h-[30px] w-[30px] cursor-pointer items-center justify-center rounded text-neutral-300 hover:bg-neutral-700/80 hover:text-white"
        onClick={() => setIsMuted(!isMuted)}
      >
        {isMuted ? <VolumeXIcon size={18} /> : <Volume2Icon size={18} />}
      </div>
    </Tooltip>
  );
};
