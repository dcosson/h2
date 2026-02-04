#!/bin/bash
# Produces several screens worth of output with sleeps between bursts.
# Usage: ./scripts/long_output.sh [sleep_seconds]
#   sleep_seconds: how long to pause between bursts (default: 2)

SLEEP=${1:-2}

echo "=== Long output test (sleep=${SLEEP}s between bursts) ==="
echo ""

# --- Burst 1 ---
echo "--- Burst 1: Numbered lines (100 lines) ---"
for i in $(seq 1 100); do
    printf "  [%03d] The quick brown fox jumps over the lazy dog\n" "$i"
done

echo ""
echo "--- Burst 1: Wide lines with padding ---"
for i in $(seq 1 30); do
    printf "  %-4d|%s|\n" "$i" "$(printf '=%.0s' $(seq 1 70))"
done

echo ""
echo ">>> Sleeping ${SLEEP}s..."
sleep "$SLEEP"

# --- Burst 2 ---
echo ""
echo "--- Burst 2: Block of prose ---"
for i in $(seq 1 20); do
    echo "  Lorem ipsum dolor sit amet, consectetur adipiscing elit. Sed do eiusmod"
    echo "  tempor incididunt ut labore et dolore magna aliqua. Ut enim ad minim"
    echo "  veniam, quis nostrud exercitation ullamco laboris nisi ut aliquip ex ea"
    echo "  commodo consequat. Duis aute irure dolor in reprehenderit in voluptate"
    echo "  velit esse cillum dolore eu fugiat nulla pariatur."
    echo ""
done

echo ">>> Sleeping ${SLEEP}s..."
sleep "$SLEEP"

# --- Burst 3 ---
echo ""
echo "--- Burst 3: Staircase pattern ---"
for i in $(seq 1 40); do
    printf "%${i}s*\n" ""
done
for i in $(seq 40 -1 1); do
    printf "%${i}s*\n" ""
done

echo ""
echo "--- Burst 3: Counting to 60 ---"
for i in $(seq 1 60); do
    printf "  tick %02d\n" "$i"
done

echo ""
echo ">>> Sleeping ${SLEEP}s..."
sleep "$SLEEP"

# --- Burst 4 ---
echo ""
echo "--- Burst 4: Final numbered burst (80 lines) ---"
for i in $(seq 1 80); do
    echo "  burst line $i of 80"
done

echo ""
echo "=== Done ==="
