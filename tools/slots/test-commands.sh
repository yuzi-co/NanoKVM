#!/bin/sh
# Every command the init invokes must be reachable at boot.
#
#   test-commands.sh <init> <unpacked-initramfs-tree>
#
# The initramfs sets PATH=/ and symlinks only a dozen busybox applets, so an
# applet compiled into the busybox binary is still "command not found" unless
# it is symlinked or invoked as /busybox <applet>. This check exists because
# losetup was compiled in, reported present by a naive check, and still failed
# a real boot with rc=127.
INIT=${1:?usage: test-commands.sh <init> <tree>}
TREE=${2:?usage: test-commands.sh <init> <tree>}

BUILTINS="if then else elif fi for while until do done case esac in return echo read export set eval exit shift local unset true false break continue cd pwd [ ] test printf : { } function . source trap wait exec"

funcs=$(grep -oE '^[a-zA-Z_][a-zA-Z0-9_]*\(\)' "$INIT" | tr -d '()' | tr '\n' ' ')

cmds=$(sed -e 's/#.*//' "$INIT" \
    | sed -e 's/||/\n/g; s/&&/\n/g; s/;/\n/g; s/|/\n/g' \
    | sed -e 's/^[[:space:]]*//' -e 's/^!\s*//' \
    | awk '{print $1}' \
    | grep -E '^[a-zA-Z_/][a-zA-Z0-9_./-]*$' \
    | sort -u)

missing=""
for c in $cmds; do
    case " $BUILTINS " in *" $c "*) continue;; esac
    case " $funcs "    in *" $c "*) continue;; esac
    case "$c" in *=*) continue;; esac
    if [ "${c#/}" != "$c" ]; then t="$TREE$c"; else t="$TREE/$c"; fi
    # -L as well as -e: the applet symlinks point at the absolute path
    # /busybox, which dangles when inspected outside the device but resolves
    # correctly at runtime.
    [ -e "$t" ] || [ -L "$t" ] || missing="$missing $c"
done

if [ -z "$missing" ]; then
    exit 0
fi
echo "  unreachable with PATH=/ :$missing"
exit 1
