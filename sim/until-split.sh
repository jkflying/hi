#!/bin/bash
# Repeat a traced sim run until a call splits, and keep that run's logs in sim/out/split. Same arguments and
# environment as run.sh. Attempt n runs with SEED=n, so a split can be rerun with its seed.
cd "$(dirname "$0")"
for i in $(seq 1 ${TRIES:-30}); do
  SEED=$i TRACE=${TRACE:-1} ./run.sh "$@" > /dev/null 2>&1
  echo "SEED=$i: $(grep -h RESULT out/run.txt | sed 's/.*call/call/' | paste -sd ';')"
  grep -q split out/run.txt || continue
  rm -rf out/split; mkdir out/split; cp out/run.txt out/hi.log out/all.txt out/moz-* out/split/ 2>/dev/null
  echo "split with SEED=$i, logs in sim/out/split"; exit 0
done
echo "no split in ${TRIES:-30} runs"; exit 1
