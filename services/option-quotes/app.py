"""Quote-only bridge to the official Futu SDK. No scheduler or trade context."""
from __future__ import annotations

import atexit
import csv
import io
import os
import re
import threading
import time
import urllib.request
from datetime import datetime, timezone
from decimal import Decimal, InvalidOperation, ROUND_HALF_UP
from zoneinfo import ZoneInfo

from flask import Flask, jsonify, request
from futu import Market, OpenQuoteContext, OptionType, RET_OK, SecurityType, SysConfig

SysConfig.enable_console_log(False)
SysConfig.set_all_thread_daemon(True)
EASTERN = ZoneInfo("America/New_York")
SYMBOL = re.compile(r"^US\.[A-Z][A-Z0-9.-]{0,14}$")
CODE = re.compile(r"^US\.[A-Z0-9.-]{1,80}$")


class QuoteError(Exception):
    def __init__(self, code, message, retryable=False):
        super().__init__(message)
        self.code, self.message, self.retryable = code, message, retryable


def decimal(value, *, signed=False, integer=False):
    """Preserve missing upstream fields; zero is an actual value, not missing."""
    if value is None or isinstance(value, bool):
        return None
    try:
        n = Decimal(str(value))
        if not n.is_finite() or (not signed and n < 0):
            return None
        if abs(n) >= Decimal("1e15") or (integer and n != n.to_integral()):
            return None
        n = n.quantize(Decimal("0.000000001"), rounding=ROUND_HALF_UP)
        return format(n, "f").rstrip("0").rstrip(".") or "0"
    except (InvalidOperation, ValueError, TypeError):
        return None


def source_time(value):
    if not isinstance(value, str) or not value or value == "N/A":
        return None
    try:
        at = datetime.fromisoformat(value)
        # update_time is the last price timestamp, NOT the bid/ask timestamp.
        if at.tzinfo is None:
            at = at.replace(tzinfo=EASTERN)
        return at.astimezone(timezone.utc).isoformat().replace("+00:00", "Z")
    except ValueError:
        return None


def map_error(message):
    # Account-specific verification URLs and IDs never leave this bridge.
    text = str(message).lower()
    if any(word in text for word in ("问卷", "协议确认", "questionnaire", "agreement")):
        return QuoteError("agreement_required", "请完成富途 API 问卷及协议确认，然后重新登录 OpenD")
    if any(word in text for word in ("权限", "permission", "authority", "right")):
        return QuoteError("quote_permission", "美股期权行情权限不足，请查看 OpenD 行情权限")
    if any(word in text for word in ("登录", "login", "log in")):
        return QuoteError("login_required", "请登录 OpenD 并完成验证")
    if any(word in text for word in ("频率", "频繁", "frequency", "too frequent")):
        return QuoteError("rate_limited", "富途请求限频，本次任务将有限重试", True)
    if any(word in text for word in ("超时", "timeout", "连接", "connect")):
        return QuoteError("connection_error", "富途行情连接中断或超时", True)
    return QuoteError("upstream_error", "富途未能返回请求的数据，请检查代码及 OpenD 状态")


class Adapter:
    def __init__(self, factory=None, clock=time.monotonic, wait=time.sleep):
        self.factory = factory or (lambda: OpenQuoteContext(
            host=os.getenv("FUTU_HOST", "127.0.0.1"),
            port=int(os.getenv("FUTU_PORT", "11111")),
        ))
        self.context = None
        self.lock = threading.Lock()
        self.clock, self.wait = clock, wait
        self.next_call = {}
        self.instruments = {}
        self.catalog_at = 0
        self.market = "unknown"
        self.verified = False
        self.english_at = 0
        self.english_names = {}

    def close(self):
        if self.context is not None:
            self.context.close()
            self.context = None

    def call(self, method, *args, **kwargs):
        if self.context is None:
            try:
                self.context = self.factory()
            except Exception:
                raise QuoteError("opend_unavailable", "无法连接 OpenD，请检查登录和内部端口", True) from None
        interval = {"get_option_chain": 3.3, "get_option_expiration_date": 3.3,
                    "get_market_snapshot": 1.05, "get_stock_basicinfo": 3.3}.get(method, 0)
        wait = self.next_call.get(method, 0) - self.clock()
        if wait > 0:
            self.wait(wait)
        self.next_call[method] = self.clock() + interval
        try:
            result, data = getattr(self.context, method)(*args, **kwargs)
        except Exception:
            raise QuoteError("sdk_error", "OpenD 请求失败，请检查连接", True) from None
        if result != RET_OK:
            raise map_error(data)
        return data

    def state(self):
        data = self.call("get_global_state")
        self.market = str(data.get("market_us", "unknown"))
        return {"connected": bool(data.get("qot_logined")),
                "version": str(data.get("server_ver", "")),
                "market_state": self.market,
                "us_options": "verified" if self.verified else "unverified",
                "checked_at": datetime.now(timezone.utc).isoformat().replace("+00:00", "Z")}

    def stock_identity(self, code):
        if code in self.instruments:
            return self.instruments[code]
        data = self.call("get_stock_basicinfo", Market.US, code_list=[code])
        for row in data.to_dict("records"):
            value = self.instrument(row)
            if value:
                self.instruments[value["code"]] = value
        if code not in self.instruments:
            raise QuoteError("unsupported_underlying", "仅支持美股股票和 ETF 的标准 Put／Call")
        return self.instruments[code]

    @staticmethod
    def instrument(row):
        code, kind = str(row.get("code", "")), str(row.get("stock_type", ""))
        if not SYMBOL.fullmatch(code) or kind not in ("STOCK", "ETF"):
            return None
        name = str(row.get("name", code))
        return {"code": code, "name": name, "english_name": str(row.get("name_en", "")),
                "aliases": "", "kind": kind, "currency": "USD"}

    def search(self, query):
        query = query.strip().lower().removeprefix("us.")
        if SYMBOL.fullmatch("US." + query.upper()):
            try:
                value = self.stock_identity("US." + query.upper())
                return {"items": [value]}
            except QuoteError as error:
                if error.code not in ("upstream_error", "unsupported_underlying"):
                    raise
        # Static names only, on explicit searches. No quote or chain requests.
        if not self.catalog_at or self.clock() - self.catalog_at > 86400:
            for kind in (SecurityType.STOCK, SecurityType.ETF):
                frame = self.call("get_stock_basicinfo", Market.US, stock_type=kind)
                for row in frame.to_dict("records"):
                    value = self.instrument(row)
                    if value:
                        self.instruments[value["code"]] = value
            self.catalog_at = self.clock()
        # OpenD's name follows its UI language. Supplement *names only* from
        # Nasdaq's public symbol directory when an English-name search needs it.
        if query.isascii() and not any(all(word in " ".join(v.values()).lower() for word in query.split()) for v in self.instruments.values()):
            self.load_english_names()
            for code, value in self.instruments.items():
                if code in self.english_names:
                    value["english_name"] = self.english_names[code]
        out = []
        for value in self.instruments.values():
            text = " ".join(value.values()).lower()
            if all(word in text for word in query.split()):
                out.append(value)
        out.sort(key=lambda value: (value["code"].lower() != "us." + query, value["code"]))
        return {"items": out[:40]}

    def load_english_names(self):
        if self.english_at and self.clock() - self.english_at < 86400:
            return
        names = {}
        try:
            for filename, field in (("nasdaqlisted.txt", "Symbol"), ("otherlisted.txt", "ACT Symbol")):
                url = "https://www.nasdaqtrader.com/dynamic/SymDir/" + filename
                req = urllib.request.Request(url, headers={"User-Agent": "OptionsWorkspace/1.0 (security name lookup)"})
                with urllib.request.urlopen(req, timeout=8) as response:
                    raw = response.read(4 * 1024 * 1024 + 1)
                if len(raw) > 4 * 1024 * 1024:
                    raise ValueError("directory too large")
                for row in csv.DictReader(io.StringIO(raw.decode("utf-8-sig")), delimiter="|"):
                    code = "US." + (row.get(field) or "")
                    if SYMBOL.fullmatch(code) and row.get("Test Issue") != "Y":
                        names[code] = (row.get("Security Name") or "")[:200]
        except Exception:
            raise QuoteError("name_directory_unavailable", "英文名称目录暂不可用，请使用股票代码或中文名称搜索") from None
        self.english_names, self.english_at = names, self.clock()

    def expirations(self, symbol):
        self.stock_identity(symbol)
        frame = self.call("get_option_expiration_date", code=symbol)
        dates = sorted(set(str(value) for value in frame["strike_time"]))
        return {"dates": dates}

    def chain(self, symbol, start, end, option_type="PUT"):
        if option_type not in ("PUT", "CALL"):
            raise QuoteError("invalid_input", "期权类型只能是 PUT 或 CALL")
        owner = self.stock_identity(symbol)
        side = OptionType.CALL if option_type == "CALL" else OptionType.PUT
        frame = self.call("get_option_chain", code=symbol, start=start, end=end, option_type=side)
        out = []
        for row in frame.to_dict("records"):
            if row.get("option_type") != option_type or row.get("stock_owner") != symbol:
                raise QuoteError("invalid_contract", "期权目录的合约身份不一致")
            lot = decimal(row.get("lot_size"), integer=True)
            standard = str(row.get("option_standard_type", "UNKNOWN"))
            strike = decimal(row.get("strike_price"))
            if strike is None:
                raise QuoteError("invalid_contract", "期权目录缺少行权价")
            # Futu option_settlement_mode means AM/PM timing, not cash/physical.
            # A standard US equity/ETF option delivers 100 shares; indices and
            # adjusted contracts are excluded, and snapshot units are rechecked.
            physical = owner["kind"] in ("STOCK", "ETF") and standard == "STANDARD" and lot == "100"
            out.append({"code": str(row["code"]), "symbol": symbol,
                        "name": str(row.get("name", row["code"])), "expiry": str(row["strike_time"]),
                        "strike": strike, "multiplier": int(lot or 0), "type": option_type,
                        "standard": standard, "settlement": "PHYSICAL" if physical else "UNKNOWN",
                        "currency": "USD"})
        return {"items": out}

    def quotes(self, contracts, symbol):
        codes = [contract["code"] for contract in contracts]
        frame = self.call("get_market_snapshot", [symbol] + codes)
        rows = {str(row["code"]): row for row in frame.to_dict("records")}
        if len(rows) != len(codes) + 1 or any(code not in rows for code in codes):
            raise QuoteError("incomplete_batch", "行情批次不完整，已保留原快照", True)
        stock = rows[symbol]
        out = []
        for contract in contracts:
            row = rows[contract["code"]]
            if contract.get("type") not in ("PUT", "CALL") or not row.get("option_valid") or row.get("stock_owner") != symbol or row.get("option_type") != contract["type"]:
                raise QuoteError("invalid_snapshot", "期权快照身份校验失败")
            if str(row.get("strike_time")) != contract["expiry"] or decimal(row.get("option_strike_price")) != contract["strike"]:
                raise QuoteError("invalid_snapshot", "期权快照日期或行权价与目录不一致")
            fields = {"bid": "bid_price", "ask": "ask_price", "bid_size": "bid_vol", "ask_size": "ask_vol",
                      "volume": "volume", "oi": "option_open_interest", "iv": "option_implied_volatility",
                      "delta": "option_delta", "last_price": "last_price"}
            quote = {key: decimal(row.get(source), signed=key == "delta", integer=key in ("volume", "oi", "bid_size", "ask_size"))
                     for key, source in fields.items()}
            status = "suspended" if row.get("suspension") else "ok"
            if contract["standard"] == "STANDARD" and any(decimal(row.get(field)) != "100" for field in ("option_contract_size", "option_contract_multiplier", "lot_size")):
                status = "metadata_invalid"
            quote.update({"contract": contract, "last_trade_at": source_time(row.get("update_time")),
                          "quote_at": None, "status": status})
            out.append(quote)
        self.verified = True
        return {"quotes": out, "underlying": decimal(stock.get("last_price")), "market_state": self.market}


def create_app(adapter=None):
    app = Flask(__name__)
    app.config["MAX_CONTENT_LENGTH"] = 256 * 1024
    bridge = adapter or Adapter()
    app.extensions["quotes"] = bridge
    atexit.register(bridge.close)

    @app.get("/healthz")
    def health():
        return jsonify(status="ok")

    @app.post("/v1/<action>")
    def rpc(action):
        data = request.get_json()
        if not isinstance(data, dict):
            return jsonify(code="invalid_input", message="请求格式不正确", retryable=False), 400
        fields = {"state": set(), "instruments": {"query"}, "expirations": {"symbol"},
                  "chain": {"symbol", "from", "to"}, "quotes": {"symbol", "contracts"}}
        optional_fields = {"option_type"} if action == "chain" else set()
        if action not in fields or not fields[action].issubset(data) or set(data) - fields[action] - optional_fields:
            return jsonify(code="invalid_input", message="请求字段不正确", retryable=False), 400
        if "symbol" in data and (not isinstance(data["symbol"], str) or not SYMBOL.fullmatch(data["symbol"])):
            return jsonify(code="invalid_input", message="股票代码不正确", retryable=False), 400
        if action == "instruments" and (not isinstance(data["query"], str) or not 1 <= len(data["query"]) <= 48):
            return jsonify(code="invalid_input", message="搜索内容不正确", retryable=False), 400
        if action == "chain":
            if data.get("option_type", "PUT") not in ("PUT", "CALL"):
                return jsonify(code="invalid_input", message="期权类型只能是 PUT 或 CALL", retryable=False), 400
            try:
                start, end = (datetime.strptime(data[key], "%Y-%m-%d") for key in ("from", "to"))
                if not 0 <= (end - start).days <= 29:
                    raise ValueError()
            except (ValueError, TypeError):
                return jsonify(code="invalid_input", message="目录日期范围不正确", retryable=False), 400
        if action == "quotes":
            values = data["contracts"]
            if (not isinstance(values, list) or not 1 <= len(values) <= 399 or
                    any(not isinstance(value, dict) or not CODE.fullmatch(str(value.get("code", ""))) or value.get("symbol") != data["symbol"] or value.get("type") not in ("PUT", "CALL") for value in values)):
                return jsonify(code="invalid_input", message="行情批次不正确", retryable=False), 400
        if not bridge.lock.acquire(timeout=2):
            return jsonify(code="busy", message="行情请求队列繁忙", retryable=True), 503
        try:
            if action == "state":
                result = bridge.state()
            elif action == "instruments":
                result = bridge.search(data["query"])
            elif action == "expirations":
                result = bridge.expirations(data["symbol"])
            elif action == "chain":
                result = bridge.chain(data["symbol"], data["from"], data["to"], data.get("option_type", "PUT"))
            else:
                result = bridge.quotes(data["contracts"], data["symbol"])
            return jsonify(result)
        except QuoteError as error:
            return jsonify(code=error.code, message=error.message, retryable=error.retryable), 503
        except Exception:
            # Do not expose SDK responses containing account identifiers or URLs.
            return jsonify(code="adapter_error", message="行情字段转换失败，已保留原快照", retryable=False), 503
        finally:
            bridge.lock.release()

    return app


if __name__ == "__main__":
    from waitress import serve
    serve(create_app(), host=os.getenv("QUOTES_HOST", "127.0.0.1"),
          port=int(os.getenv("QUOTES_PORT", "5053")), threads=4,
          connection_limit=16, channel_timeout=100, max_request_body_size=256 * 1024)
