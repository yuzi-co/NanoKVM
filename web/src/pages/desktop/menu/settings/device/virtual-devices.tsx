import { useEffect, useState } from 'react';
import { Progress, Switch, Tooltip } from 'antd';
import { useTranslation } from 'react-i18next';

import { getHidMode } from '@/api/hid.ts';
import * as api from '@/api/virtual-device.ts';
import type {
  VirtualDeviceName,
  VirtualDevices as VirtualDevicesState
} from '@/api/virtual-device.ts';

export const VirtualDevices = () => {
  const { t } = useTranslation();

  const [isHidOnlyMode, setIsHidOnlyMode] = useState(false);
  const [devices, setDevices] = useState<VirtualDevicesState | null>(null);
  const [loading, setLoading] = useState<'' | VirtualDeviceName>('');
  const [refusal, setRefusal] = useState('');

  useEffect(() => {
    getHidOnlyMode();
    getVirtualDevice();
  }, []);

  async function getHidOnlyMode() {
    try {
      const rsp = await getHidMode();
      if (rsp.code !== 0) {
        console.log(rsp.msg);
        return;
      }
      setIsHidOnlyMode(rsp.data.mode === 'hid-only');
    } catch (err) {
      console.log(err);
    }
  }

  async function getVirtualDevice() {
    try {
      const rsp = await api.getVirtualDevice();
      if (rsp.code !== 0) {
        console.log(rsp.msg);
        setRefusal(t('settings.device.endpoints.error'));
        return;
      }

      setDevices(rsp.data);
    } catch (err) {
      console.log(err);
      setRefusal(t('settings.device.endpoints.error'));
    }
  }

  async function update(device: VirtualDeviceName) {
    if (loading) return;
    setLoading(device);
    setRefusal('');

    try {
      const rsp = await api.updateVirtualDevice(device);
      if (rsp.code !== 0) {
        // The server owns the numbers, so show its sentence rather than
        // recomputing the budget here and risking a different answer.
        setRefusal(rsp.msg);
        return;
      }

      await getVirtualDevice();
    } catch (err) {
      console.log(err);
      // Toggling restarts the USB gadget, so a request can go missing while
      // it rebuilds. Say so, rather than leaving the switch snap back with
      // no explanation.
      setRefusal(t('settings.device.endpoints.error'));
    } finally {
      setLoading('');
    }
  }

  if (isHidOnlyMode) {
    return (
      <div className="flex items-center justify-between space-x-10">
        <div className="flex flex-col space-y-1">
          <span>{t('settings.device.hidOnly')}</span>
          <span className="text-xs text-neutral-500">{t('settings.device.hidOnlyDesc')}</span>
        </div>

        <Switch checked={true} disabled={true} />
      </div>
    );
  }

  const free = devices ? devices.total - devices.used : 0;

  function row(device: VirtualDeviceName) {
    if (!devices) return null;

    const state = devices[device];
    const fits = state.enabled || state.cost <= free;

    return (
      <div className="flex items-center justify-between">
        <div className="flex flex-col space-y-1">
          <span>{t(`settings.device.${device}`)}</span>
          <span className="text-xs text-neutral-500">{t(`settings.device.${device}Desc`)}</span>

          {device === 'console' && (
            <span className="text-xs text-amber-500">{t('settings.device.consoleTip')}</span>
          )}

          {state.enabled && !state.active && (
            <span className="text-xs text-amber-500">
              {t('settings.device.endpoints.inactive')}
            </span>
          )}
        </div>

        <div className="flex items-center space-x-3">
          <span id={`endpoint-cost-${device}`} className="text-xs text-neutral-500">
            {state.enabled
              ? t('settings.device.endpoints.cost', { cost: state.cost })
              : t('settings.device.endpoints.needs', { cost: state.cost, free })}
          </span>

          {/* antd clones the Tooltip child directly, and a disabled native
              <button> suppresses mouse events, so the tooltip on the Switch
              itself would never open. Wrapping it in a span that stays
              enabled gives the Tooltip a target that still receives hover. */}
          <Tooltip title={fits ? '' : t('settings.device.endpoints.full')}>
            <span className="inline-block">
              <Switch
                checked={state.enabled}
                disabled={!fits}
                loading={loading === device}
                onChange={() => update(device)}
                aria-describedby={`endpoint-cost-${device}`}
              />
            </span>
          </Tooltip>
        </div>
      </div>
    );
  }

  return (
    <>
      {devices && (
        <div className="flex flex-col space-y-1">
          <div className="flex items-center justify-between">
            <span>{t('settings.device.endpoints.title')}</span>
            <span className="text-xs text-neutral-500">
              {t('settings.device.endpoints.used', {
                used: devices.used,
                total: devices.total
              })}
            </span>
          </div>

          <Progress
            percent={(devices.used / devices.total) * 100}
            showInfo={false}
            size="small"
            strokeColor={free === 0 ? '#f59e0b' : undefined}
          />

          <span className="text-xs text-neutral-500">{t('settings.device.endpoints.explain')}</span>
        </div>
      )}

      {refusal && <span className="text-xs text-red-500">{refusal}</span>}

      {row('console')}
      {row('disk')}
      {row('network')}
      {row('audio')}
    </>
  );
};
