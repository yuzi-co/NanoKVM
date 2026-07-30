import { useEffect, useRef } from 'react';
import { Button, notification } from 'antd';
import { useAtom } from 'jotai';
import { useTranslation } from 'react-i18next';

import { getHidStatus, reset as resetHid } from '@/api/hid.ts';
import { applyMouseMode } from '@/lib/mouse-mode.ts';
import { client } from '@/lib/websocket.ts';
import { mouseModeAtom } from '@/jotai/mouse.ts';

import { HidDeviceStatus, isAbsoluteMouseStalled } from './model.ts';

const NOTIFICATION_KEY = 'absolute_mouse_stalled';
const POLL_INTERVAL_MS = 10_000;

// AbsoluteMouseWarning tells the operator that absolute mouse reports are going
// nowhere, and offers the mode that works.
//
// Nothing else can say this. The device node is present, the USB gadget is
// bound and configured, and the keyboard keeps working - so every other signal
// reads healthy while the pointer does not move.
export const AbsoluteMouseWarning = () => {
  const { t } = useTranslation();
  const [api, contextHolder] = notification.useNotification();
  const [mouseMode, setMouseMode] = useAtom(mouseModeAtom);
  const isOpen = useRef(false);
  const isRecovering = useRef(false);

  useEffect(() => {
    // Only absolute mode uses the endpoint that stalls, so relative mode has
    // nothing to poll for and should cost the device nothing.
    if (mouseMode !== 'absolute') {
      if (isOpen.current) {
        api.destroy(NOTIFICATION_KEY);
        isOpen.current = false;
      }
      return;
    }

    let cancelled = false;

    function check() {
      getHidStatus()
        .then((rsp) => {
          if (cancelled || rsp.code !== 0) return;

          const devices: HidDeviceStatus[] = rsp.data?.devices ?? [];
          if (isAbsoluteMouseStalled(devices)) {
            open();
          } else if (isOpen.current) {
            api.destroy(NOTIFICATION_KEY);
            isOpen.current = false;
          }
        })
        .catch(() => {
          // A failed status call is not worth telling the operator about. It
          // says nothing about the mouse, and this runs every ten seconds.
        });
    }

    function open() {
      if (isOpen.current) return;
      isOpen.current = true;

      api.warning({
        key: NOTIFICATION_KEY,
        message: t('mouse.absoluteStalled'),
        description: t('mouse.absoluteStalledDesc'),
        placement: 'topRight',
        duration: null,
        // Two remedies, because either can be the right one and the server
        // cannot tell which. Recovering USB is offered first: it keeps the
        // mouse mode the operator chose.
        btn: (
          <div className="flex gap-2">
            <Button onClick={recoverUsb} loading={isRecovering.current}>
              {t('mouse.resetHid')}
            </Button>
            <Button type="primary" onClick={switchToRelative}>
              {t('mouse.useRelative')}
            </Button>
          </div>
        ),
        onClose: () => {
          isOpen.current = false;
        }
      });
    }

    function switchToRelative() {
      api.destroy(NOTIFICATION_KEY);
      isOpen.current = false;
      applyMouseMode('relative', setMouseMode);
    }

    // The same recovery the mouse menu offers: re-enumerate the USB gadget and
    // let the target bind its HID interfaces again. The websocket goes down
    // with it, so it closes first and reconnects after.
    function recoverUsb() {
      if (isRecovering.current) return;
      isRecovering.current = true;

      client.close();
      resetHid().finally(() => {
        client.connect();
        isRecovering.current = false;
        api.destroy(NOTIFICATION_KEY);
        isOpen.current = false;
      });
    }

    check();
    const timer = setInterval(check, POLL_INTERVAL_MS);

    return () => {
      cancelled = true;
      clearInterval(timer);
    };
  }, [mouseMode, api, t, setMouseMode]);

  return <>{contextHolder}</>;
};
