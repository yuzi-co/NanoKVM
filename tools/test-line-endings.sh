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

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

fails=0
note() { printf '  %-60s %s\n' "$1" "$2"; [ "$2" = FAIL ] && fails=$((fails + 1)); return 0; }

# The candidate list, built once. device_files walks every tracked and untracked
# file, so calling it per section doubled the runtime of a check that has to stay
# quick enough that people still run it.
LIST="$WORK/list"

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
device_files > "$LIST"

bad=0
while IFS= read -r f; do
    [ -f "$f" ] || continue
    c=$(tr -cd '\r' < "$f" | wc -c)
    if [ "$c" -gt 0 ]; then
        note "$f has $c CR" FAIL
        bad=$((bad + 1))
    fi
done < "$LIST"
[ "$bad" -eq 0 ] && note "all $(wc -l < "$LIST") device files are LF" OK

echo
echo "===== every device file is pinned by .gitattributes ====="
# One git check-attr for the whole list, not one per file. Per-file was correct
# and took over two minutes once the rule covered tools/**, and a check that slow
# is a check that stops being run.
# What matters is that the working-tree copy comes out with LF, which needs two
# things: git treating the file as text, and eol=lf. `text` and `text=auto` both
# satisfy the first - auto detects text and a file with a shebang always is one -
# so requiring the literal word "set" rejected a correct rule.
bad=0
git check-attr text eol --stdin < "$LIST" > "$WORK/attrs" 2>/dev/null
while IFS= read -r f; do
    [ -n "$f" ] || continue
    t=$(grep -F "$f: text: " "$WORK/attrs" | sed 's/.*: text: //')
    e=$(grep -F "$f: eol: " "$WORK/attrs" | sed 's/.*: eol: //')
    case "$t" in
        set|auto) ;;
        *) note "$f: text is '$t', so git may not convert it" FAIL; bad=$((bad + 1)); continue ;;
    esac
    if [ "$e" != "lf" ]; then
        note "$f: eol is '$e', so a Windows checkout still gets CRLF" FAIL
        bad=$((bad + 1))
    fi
done < "$LIST"
[ "$bad" -eq 0 ] && note "all $(wc -l < "$LIST") device files check out as LF" OK

echo
echo "===== binaries are not treated as text ====="
# The reverse mistake corrupts a kernel module or a shared object instead.
git ls-files '*.ko' '*.so' 'kvmapp/kvm_system/*' 'kvmapp/kvm_new_app' 'tools/nanokvm_update_edid/nanokvm_update_edid' 2>/dev/null > "$WORK/bins"
git check-attr text --stdin < "$WORK/bins" > "$WORK/binattrs" 2>/dev/null
while IFS= read -r line; do
    case "$line" in
        *": text: set") note "${line%%: text: *} would be line-ending converted" FAIL ;;
    esac
done < "$WORK/binattrs"
note "binary payloads are excluded" OK

echo
if [ "$fails" -eq 0 ]; then
    echo "===== line endings are safe ====="
else
    echo "===== $fails problem(s): a device script will break on a Windows checkout ====="
    exit 1
fi
