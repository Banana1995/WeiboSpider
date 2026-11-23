import math
import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from app import Adapter, QuoteError, create_app, decimal, map_error, source_time


class AdapterTests(unittest.TestCase):
    def test_decimal_missing_zero_and_precision(self):
        for value in (None, math.nan, math.inf, "N/A", "", -1):
            self.assertIsNone(decimal(value))
        self.assertEqual("0", decimal(0))
        self.assertEqual("3.45", decimal(3.45))
        self.assertEqual("-0.123456789", decimal(-0.1234567894, signed=True))
        self.assertIsNone(decimal(1.5, integer=True))

    def test_source_timestamp_handles_both_dst_offsets(self):
        self.assertEqual("2026-11-13T20:00:00Z", source_time("2026-11-13 15:00:00"))
        self.assertEqual("2026-06-12T19:00:00Z", source_time("2026-06-12 15:00:00"))
        self.assertIsNone(source_time("N/A"))

    def test_private_error_is_never_forwarded(self):
        error = map_error("用户 12345 需问卷评估 https://example.test/?secret=abc")
        self.assertEqual("agreement_required", error.code)
        self.assertNotIn("12345", error.message)
        self.assertNotIn("secret", error.message)

    def test_health_and_invalid_inputs_do_not_connect(self):
        def forbidden():
            raise AssertionError("unexpected SDK connection")
        client = create_app(Adapter(factory=forbidden)).test_client()
        self.assertEqual(200, client.get("/healthz").status_code)
        for path, payload in (("chain", {"symbol": "US.AAPL", "from": "2026-01-01", "to": "2026-03-01"}),
                              ("chain", {"symbol": "US.AAPL", "from": "2026-01-01", "to": "2026-01-20", "option_type": "ALL"}),
                              ("quotes", {"symbol": "US.AAPL", "contracts": []}),
                              ("instruments", {"query": ""}), ("trade", {})):
            self.assertEqual(400, client.post("/v1/" + path, json=payload).status_code)

    def test_shared_limiter_and_lazy_connection(self):
        calls, waits = [], []
        class SDK:
            def get_option_chain(self):
                calls.append("chain")
                return 0, []
            def close(self):
                pass
        bridge = Adapter(factory=SDK, clock=lambda: 10, wait=waits.append)
        self.assertIsNone(bridge.context)
        bridge.call("get_option_chain")
        bridge.call("get_option_chain")
        self.assertEqual(["chain", "chain"], calls)
        self.assertAlmostEqual(3.3, waits[0])

    def test_english_search_is_verified_against_futu_identities(self):
        import pandas as pd
        class SDK:
            def get_stock_basicinfo(self, market, stock_type="STOCK", code_list=None):
                rows = [] if code_list else [{"code": "US.COST", "name": "开市客", "stock_type": "STOCK"}]
                return 0, pd.DataFrame(rows)
            def close(self):
                pass
        bridge = Adapter(factory=SDK, wait=lambda seconds: None)
        bridge.load_english_names = lambda: bridge.english_names.update({"US.COST": "Costco Wholesale Corporation", "US.FAKE": "Costco unverified"})
        results = bridge.search("Costco")["items"]
        self.assertEqual(["US.COST"], [row["code"] for row in results])
        self.assertEqual("开市客", results[0]["name"])

    def test_cached_name_search_skips_a_busy_quote_lock(self):
        def forbidden():
            raise AssertionError("unexpected SDK connection")
        bridge = Adapter(factory=forbidden, clock=lambda: 10)
        bridge.instruments = {
            "US.TME": {"code": "US.TME", "name": "腾讯音乐", "english_name": "", "aliases": "", "kind": "STOCK", "currency": "USD"},
            "US.TCEHY": {"code": "US.TCEHY", "name": "腾讯控股(ADR)", "english_name": "", "aliases": "", "kind": "STOCK", "currency": "USD"},
        }
        bridge.catalog_at = 10
        bridge.lock.acquire()
        try:
            response = create_app(bridge).test_client().post("/v1/instruments", json={"query": "腾讯"})
        finally:
            bridge.lock.release()
        self.assertEqual(200, response.status_code)
        self.assertEqual(["US.TCEHY", "US.TME"], [row["code"] for row in response.get_json()["items"]])

    def test_call_chain_and_quotes_keep_direction_and_bid(self):
        import pandas as pd
        seen = []
        class SDK:
            wrong_side = False
            def get_stock_basicinfo(self, *args, **kwargs):
                return 0, pd.DataFrame([{"code": "US.AAPL", "name": "苹果", "stock_type": "STOCK"}])
            def get_option_chain(self, **kwargs):
                seen.append(kwargs["option_type"])
                return 0, pd.DataFrame([{"code": "US.AAPL261218C110000", "stock_owner": "US.AAPL", "option_type": kwargs["option_type"], "strike_time": "2026-12-18", "strike_price": 110, "lot_size": 100, "option_standard_type": "STANDARD"}])
            def get_market_snapshot(self, codes):
                return 0, pd.DataFrame([
                    {"code": "US.AAPL", "last_price": 100},
                    {"code": "US.AAPL261218C110000", "option_valid": True, "stock_owner": "US.AAPL", "option_type": "PUT" if self.wrong_side else "CALL", "strike_time": "2026-12-18", "option_strike_price": 110, "option_contract_size": 100, "option_contract_multiplier": 100, "lot_size": 100, "bid_price": 3, "ask_price": 3.2, "option_delta": .3, "suspension": False},
                ])
            def close(self):
                pass
        sdk = SDK();bridge = Adapter(factory=lambda: sdk, wait=lambda seconds: None)
        contracts = bridge.chain("US.AAPL", "2026-12-18", "2026-12-18", "CALL")["items"]
        self.assertEqual(["CALL"], seen)
        self.assertEqual("CALL", contracts[0]["type"])
        quote = bridge.quotes(contracts, "US.AAPL")["quotes"][0]
        self.assertEqual("3", quote["bid"]);self.assertEqual("3.2", quote["ask"])
        self.assertEqual("0.3", quote["delta"])
        sdk.wrong_side = True
        with self.assertRaises(QuoteError) as raised:
            bridge.quotes(contracts, "US.AAPL")
        self.assertEqual("invalid_snapshot", raised.exception.code)


if __name__ == "__main__":
    unittest.main()
