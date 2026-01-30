#!/bin/bash

# Start the Redis chaos engineering demo environment

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

echo "🚀 Starting Redis Chaos Engineering Demo Environment"
echo ""

# Check if docker-compose is available
if command -v docker-compose &> /dev/null; then
    COMPOSE_CMD="docker-compose"
elif command -v docker &> /dev/null && docker compose version &> /dev/null; then
    COMPOSE_CMD="docker compose"
else
    echo "❌ Error: docker-compose or docker compose not found"
    exit 1
fi

# Stop any existing containers
echo "🧹 Cleaning up existing containers..."
$COMPOSE_CMD down -v 2>/dev/null || true

# Build and start
echo "🔨 Building demo application..."
$COMPOSE_CMD build

echo "🚀 Starting services..."
$COMPOSE_CMD up -d

# Wait for services to be ready
echo "⏳ Waiting for services to be ready..."
sleep 5

# Check Redis master
echo -n "   Redis master: "
if docker exec redis-master redis-cli -a dev-password PING 2>/dev/null | grep -q PONG; then
    echo "✅ Ready"
else
    echo "❌ Not ready"
fi

# Check Redis replica
echo -n "   Redis replica: "
if docker exec redis-replica redis-cli -a dev-password PING 2>/dev/null | grep -q PONG; then
    echo "✅ Ready"
else
    echo "⏳ Still syncing..."
fi

# Check demo app
echo -n "   Demo app: "
for i in {1..30}; do
    if curl -s http://localhost:3400/health | grep -q OK; then
        echo "✅ Ready"
        break
    fi
    if [ $i -eq 30 ]; then
        echo "❌ Not ready after 30s"
    fi
    sleep 1
done

echo ""
echo "═══════════════════════════════════════════════════════════════════"
echo "                    Demo Environment Ready!"
echo "═══════════════════════════════════════════════════════════════════"
echo ""
echo "📊 Services:"
echo "   • Redis Master:  localhost:6379 (password: dev-password)"
echo "   • Redis Replica: localhost:6380 (password: dev-password)"
echo "   • Demo App:      http://localhost:3400"
echo ""
echo "🔗 Useful endpoints:"
echo "   • Health:      curl http://localhost:3400/health"
echo "   • Stats:       curl http://localhost:3400/stats"
echo "   • User cache:  curl http://localhost:3400/user/1"
echo "   • Products:    curl http://localhost:3400/product/5"
echo "   • Leaderboard: curl http://localhost:3400/leaderboard"
echo "   • Sessions:    curl -X POST http://localhost:3400/session/my-session"
echo "   • Rate limit:  curl http://localhost:3400/rate-limit/client-1"
echo "   • Counter:     curl http://localhost:3400/counter/pageviews"
echo ""
echo "🔧 Run the extension:"
echo ""
echo "   export STEADYBIT_EXTENSION_ENDPOINTS_JSON='["
echo "     {\"url\":\"redis://localhost:6379\",\"password\":\"dev-password\",\"name\":\"redis-master\"},"
echo "     {\"url\":\"redis://localhost:6380\",\"password\":\"dev-password\",\"name\":\"redis-replica\"}"
echo "   ]'"
echo "   go run ."
echo ""
echo "📖 See CHAOS_EXPERIMENTS.md for experiment scenarios"
echo ""
echo "🛑 To stop: cd demo && docker compose down"
echo ""
