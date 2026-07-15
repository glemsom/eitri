#!/usr/bin/env bash
set -euo pipefail

# cloud-bubble - wrap text from stdin in a fluffy ASCII cloud

lines=()
while IFS= read -r line || [[ -n $line ]]; do
  lines+=("$line")
done

if [[ ${#lines[@]} -eq 0 ]]; then
  echo "Usage: echo 'Hello!' | $0"
  echo "   or:  $0 < file.txt"
  exit 1
fi

max_len=0
for line in "${lines[@]}"; do
  len=${#line}
  (( len > max_len )) && max_len=$len
done

bar=$(printf '%*s' "$max_len" '' | tr ' ' '-')

echo "     .--${bar}--."
for line in "${lines[@]}"; do
  echo "    (  ${line}  )"
done
echo "     '--${bar}--'"
echo "        \\"
echo "         )"
echo "        /"
echo "       |"
