#!/bin/sh
# Assert that every file which runs on the device is safe from CRLF.
#
#   test-line-endings.sh
#
# Two failures are possible, and both have happened:
#
#   a working tree file already carries CR, so deploying it breaks the device
#   a file is not covered by .gitattributes, so the next checkout gives it CRLF
#
# The second is the one that hides. A file can be correct today and wrong after
# a fresh clone on Windows, which is why the attribute is checked and not only
# the bytes. Run this after adding any script that reaches the device.
cd "$(dirname "$0")/.." || exit 1

fails=0
note() { printf '  %-60s %s\n' "$1" "$2"; [ "$2" = FAIL ] && fails=$((fails + 1)); return 0; }

# Every file the kernel has to exec, found by the only property they share: a
# shebang on the first line. A hand-written list of paths was tried first and
# missed three files in one afternoon - tools/oled/S97oled-nudge, then
# support/sg2002/build, then docker/entrypoint. Each failed differently:
#
#   S97oled-nudge      would have broken at boot on the next fresh clone
#   support/sg2002/build   exec "/bin/bash\r" -> 127, no builder image
#   docker/entrypoint      exec "/bin/sh\r"   -> "no such file or directory"
#
# None of them is on the device, and location told you nothing. The shebang did.
#
# git grep narrows the candidates quickly; the first line is then checked
# properly, because a shebang has to be on line 1 to mean anything. --untracked
# includes files not committed yet: without it this check passes while the script
# you just added is the unprotected one, which is the case it exists to catch.
device_files() {
    {
        git grep -l -I --untracked -e '^#!' 2>/dev/null
        git ls-files --cached --others --exclude-standard '*.sh' '*.py' 2>/dev/null
    } | sort -u | while read -r f; do
        [ -f "$f" ] || continue
        case "$(head -c 2 "$f" 2>/dev/null)" in
            '#!') echo "$f" ;;
            *) case "$f" in *.sh|*.py) echo "$f" ;; esac ;;
        esac
    done
}

echo "===== no device file carries a carriage return ====="
bad=0
for f in $(device_files); do
    [ -f "$f" ] || continue
    c=$(tr -cd '\r' < "$f" | wc -c)
    if [ "$c" -gt 0 ]; then
        note "$f has $c CR" FAIL
        bad=$((bad + 1))
    fi
done
[ "$bad" -eq 0 ] && note "all $(device_files | wc -l) device files are LF" OK

echo
echo "===== every device file is pinned by .gitattributes ====="
bad=0
for f in $(device_files); do
    [ -f "$f" ] || continue
    if [ "$(git check-attr text -- "$f" | awk '{print $NF}')" != "set" ]; then
        note "$f is not pinned to text/eol=lf" FAIL
        bad=$((bad + 1))
    fi
done
[ "$bad" -eq 0 ] && note "every device file resolves to text: set" OK

echo
echo "===== binaries are not treated as text ====="
# The reverse mistake corrupts a kernel module or a shared object instead.
for f in $(git ls-files '*.ko' '*.so' 'kvmapp/kvm_system/*' 'kvmapp/kvm_new_app' 2>/dev/null); do
    [ -f "$f" ] || continue
    if [ "$(git check-attr text -- "$f" | awk '{print $NF}')" = "set" ]; then
        note "$f would be line-ending converted" FAIL
    fi
done
note "binary payloads are excluded" OK

echo
if [ "$fails" -eq 0 ]; then
    echo "===== line endings are safe ====="
else
    echo "===== $fails problem(s): a device script will break on a Windows checkout ====="
    exit 1
fi
