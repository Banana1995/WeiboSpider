"""Isolated E2E SDK fixture; never copied to the production image."""
import os
import sys
import threading
from datetime import datetime, timedelta
from pathlib import Path
from zoneinfo import ZoneInfo

import pandas as pd
from flask import jsonify, request
from waitress import serve

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from app import Adapter, create_app

if os.getenv("OPTIONS_TEST_MODE") != "1":
    raise RuntimeError("fixture requires OPTIONS_TEST_MODE=1")

names = {"US.AAPL": ("苹果", "Apple Inc."), "US.MSFT": ("微软", "Microsoft Corporation"),
         "US.NVDA": ("英伟达", "NVIDIA Corporation"), "US.TSLA": ("特斯拉", "Tesla Inc.")}
today = datetime.now(ZoneInfo("America/New_York")).date()
dates = [(today + timedelta(days=35)).isoformat(), (today + timedelta(days=63)).isoformat()]
state = {"calls": 0, "quote_calls": 0, "chain_calls": 0, "put_chain_calls": 0, "call_chain_calls": 0, "shift": 0, "fail_symbol": "", "fail_type": ""}
guard = threading.Lock()


class SDK:
    def close(self):
        pass

    def get_global_state(self):
        with guard:
            state["calls"] += 1
        return 0, {"qot_logined": True, "server_ver": "fixture", "market_us": "REST"}

    def get_stock_basicinfo(self, market, stock_type="STOCK", code_list=None):
        return 0, pd.DataFrame([{"code": code, "name": name, "name_en": english, "stock_type": "STOCK"}
                                for code, (name, english) in names.items() if not code_list or code in code_list])

    def get_option_expiration_date(self, code):
        with guard:
            state["calls"] += 1
        return 0, pd.DataFrame({"strike_time": dates})

    @staticmethod
    def contracts(symbol, side):
        out = []
        for day in dates:
            samples = ((200, 3.45, 3.65), (195, 2.10, 2.20), (140, 0, .10)) if side == "PUT" else ((220, 3, 3.2), (200, 12, 12.5), (280, 0, .1))
            for strike, bid, ask in samples:
                code = symbol + day[2:].replace("-", "") + side[0] + str(strike * 1000)
                out.append({"code": code, "name": code, "stock_type": "DRVT", "lot_size": 100,
                            "stock_owner": symbol, "strike_time": day, "strike_price": strike,
                            "option_type": side, "option_standard_type": "STANDARD", "option_settlement_mode": "N/A",
                            "bid": bid, "ask": ask})
        return out

    def get_option_chain(self, code, start, end, option_type):
        with guard:
            state["calls"] += 1
            state["chain_calls"] += 1
            state["call_chain_calls" if option_type == "CALL" else "put_chain_calls"] += 1
        return 0, pd.DataFrame([row for row in self.contracts(code, option_type) if start <= row["strike_time"] <= end])

    def get_market_snapshot(self, codes):
        with guard:
            state["calls"] += 1
            state["quote_calls"] += 1
            shift, fail, fail_type = state["shift"], state["fail_symbol"], state["fail_type"]
        symbol = codes[0]
        side = "CALL" if codes[1].split(symbol)[1][6:7] == "C" else "PUT"
        if symbol == fail and (not fail_type or side == fail_type):
            return -1, "行情权限不足"
        out = [{"code": symbol, "last_price": 210}]
        for row in self.contracts(symbol, side):
            if row["code"] not in codes:
                continue
            out.append({"code": row["code"], "option_valid": True, "stock_owner": symbol, "option_type": side,
                        "strike_time": row["strike_time"], "option_strike_price": row["strike_price"],
                        "option_contract_size": 100, "option_contract_multiplier": 100, "lot_size": 100,
                        "bid_price": row["bid"] + shift if row["bid"] else 0, "ask_price": row["ask"] + shift,
                        "bid_vol": 12, "ask_vol": 20, "option_open_interest": 1234, "volume": 15,
                        "option_implied_volatility": 31, "option_delta": -.3 if side == "PUT" else .3, "last_price": row["bid"],
                        "update_time": today.isoformat() + " 15:00:00", "suspension": False})
        return 0, pd.DataFrame(out)


app = create_app(Adapter(factory=SDK, wait=lambda seconds: None))


@app.get("/test/counters")
def counters():
    with guard:
        return jsonify(state)


@app.post("/test/control")
def control():
    payload = request.get_json()
    with guard:
        for key in ("shift", "fail_symbol", "fail_type"):
            if key in payload:
                state[key] = payload[key]
    return jsonify(ok=True)


serve(app, host="127.0.0.1", port=17053, threads=4)
