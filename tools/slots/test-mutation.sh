#!/bin/sh
# Prove test-commands.sh is not vacuous.
#
#   test-mutation.sh <patched-init> <unpacked-initramfs-tree>
#
# Runs the check against the real init, then against a copy with the /busybox
# prefix stripped - the exact defect that reached a real boot as rc=127. A
# check that passes both ways is worthless, and several checks written during
# this work did exactly that before being fixed.
HERE=$(cd "$(dirname "$0")" && pwd)
INIT=${1:?usage: test-mutation.sh <init> <tree>}
TREE=${2:?usage: test-mutation.sh <init> <tree>}

echo "== shipped init (uses /busybox losetup)"
if sh "$HERE/test-commands.sh" "$INIT" "$TREE"; then
    echo "   clean  <- expected"
else
    echo "   unexpectedly flagged something"; exit 1
fi

echo
echo "== mutated init (bare 'losetup')"
sed 's|/busybox losetup|losetup|g' "$INIT" > /tmp/init.mutated
if sh "$HERE/test-commands.sh" /tmp/init.mutated "$TREE"; then
    echo "   MUTATION NOT CAUGHT - the check is vacuous"; exit 1
else
    echo "   flagged  <- the check bites"
fi

echo
echo "  mutation test passed"
