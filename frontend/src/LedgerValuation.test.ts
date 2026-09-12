// @vitest-environment jsdom
import { expect, it } from "vitest";
import { mount } from "@vue/test-utils";
import LedgerValuation from "./LedgerValuation.vue";
import type { Valuation } from "./ledger";

const value: Valuation = {
  source: "manual_snapshot",
  ledger_revision: "a".repeat(64),
  history_id: "42",
  account_id: "account",
  currency: "CNY",
  as_of: "2026-09-06",
  ledger_at: "2026-09-06T10:00:00Z",
  calculated_at: "2026-09-06T10:00:01Z",
  cash: "90071992547409.01",
  known_positions_value: "70.01",
  positions_value: "70.01",
  total_assets: "90071992547479.02",
  complete: true,
  items: [
    {
      instrument_id: "usd",
      quantity: "1.000001",
      market_value: "70.01",
      status: "prior_date",
      quote: {
        symbol: "sh900001",
        currency: "USD",
        price: "10.000001",
        date: "2026-09-04",
        quoted_at: "2026-09-04T15:00:00+08:00",
        fetched_at: "2026-09-06T09:59:59Z",
        source: "Tencent",
      },
      fx: {
        base: "USD",
        quote: "CNY",
        rate: "7.00000001",
        mode: "latest",
        requested_date: "",
        date: "2026-09-05",
        quoted_at: "2026-09-05T15:00:00+08:00",
        source: "Tencent/USDCNY",
        fetched_at: "2026-09-06T10:00:00Z",
      },
    },
  ],
};
const render = (snapshot: Valuation) =>
  mount(LedgerValuation, {
    props: {
      accountId: "account",
      value: snapshot,
      loading: false,
      error: "",
      instruments: [],
    },
  });

it("shows exact complete assets and foreign quote/FX provenance without numeric conversion", () => {
  const wrapper = render(value);
  expect(wrapper.get('[data-test="valuation-total"]').text()).toContain(
    "90071992547479.02",
  );
  for (const text of [
    "CNY",
    "10.000001 USD",
    "USD/CNY 7.00000001",
    "Tencent/USDCNY",
    "Tencent",
    "1.000001",
    "70.01",
    value.ledger_at,
    value.calculated_at,
    value.as_of,
    value.items[0]!.quote!.quoted_at,
    value.items[0]!.quote!.fetched_at,
    "2026-09-05",
    "prior_date",
    "成本不参与估值",
    "实际成交汇率不随刷新改写",
  ])
    expect(wrapper.text()).toContain(text);
  wrapper.unmount();
});

it.each([false, true])(
  "never promotes a null total or partial value to assets (complete=%s)",
  (complete) => {
    const wrapper = render({
      ...value,
      complete,
      total_assets: null,
      positions_value: null,
      items: [
        {
          instrument_id: "missing",
          quantity: "2.000000",
          quote: null,
          fx: null,
          market_value: null,
          status: "unavailable",
          error_code: "quote_unavailable",
        },
        {
          instrument_id: "closed",
          quantity: "0.000000",
          quote: null,
          fx: null,
          market_value: "0.00",
          status: "closed",
        },
      ],
    });
    expect(wrapper.get('[data-test="valuation-total"]').text()).toContain("—");
    expect(wrapper.text()).toContain("不能完整估值");
    expect(wrapper.text()).toContain("不完整，仅已知部分");
    const rows = wrapper.findAll("tbody tr");
    expect(rows[0]!.text()).toContain("未知 / 不适用 CNY");
    expect(rows[0]!.text()).toContain("证券行情暂不可用");
    expect(rows[1]!.text()).toContain("0.00 CNY");
    expect(rows[1]!.text()).toContain("已清仓");
    wrapper.unmount();
  },
);

it("does not show a non-null total when the response is incomplete", () => {
  const wrapper = render({ ...value, complete: false });
  expect(wrapper.get('[data-test="valuation-total"]').text()).not.toContain(
    value.total_assets!,
  );
  expect(wrapper.text()).toContain("本次估值不完整，不写入历史记录");
  expect(wrapper.text()).not.toContain("此快照已保存");
  wrapper.unmount();
});

it("only confirms a complete snapshot saved when its history ID is returned", () => {
  const saved = render(value);
  expect(saved.text()).toContain("此快照已保存为历史记录 #42");
  saved.unmount();
  const missing = render({ ...value, history_id: undefined });
  expect(missing.text()).toContain("只读参考估值，不改变账本");
  expect(missing.text()).not.toContain("此快照已保存为历史记录");
  missing.unmount();
});

it("labels a retained complete total as expired throughout refresh and failure", async () => {
  const wrapper = render(value);
  await wrapper.setProps({ loading: true });
  expect(wrapper.get("h2").text()).toBe("过期估值快照");
  expect(wrapper.get('[data-test="valuation-total"]').text()).toContain(
    "上次快照总资产（过期）",
  );
  await wrapper.setProps({ loading: false, error: "读取失败" });
  expect(wrapper.text()).toContain("不能视为当前资产");
  expect(wrapper.get('[data-test="valuation-total"]').text()).toContain(
    value.total_assets!,
  );
  await wrapper.setProps({
    error: "",
    value: { ...value, total_assets: "0.00" },
  });
  expect(wrapper.get("h2").text()).toBe("当前参考估值");
  expect(wrapper.get('[data-test="valuation-total"]').text()).toContain("0.00");
  wrapper.unmount();
});

it.each([
  ["unsupported_instrument", "不支持该市场或证券代码行情"],
  ["currency_mismatch", "证券登记币种与报价币种不匹配"],
  ["quote_timeout", "证券行情查询超时"],
  ["fx_unavailable", "当前参考汇率暂不可用"],
  ["fx_timeout", "当前参考汇率查询超时"],
  ["future_code", "估值数据不可用"],
])("shows a Chinese item error for %s", (error_code, label) => {
  const wrapper = render({
    ...value,
    complete: false,
    total_assets: null,
    items: [
      {
        ...value.items[0]!,
        status: "unavailable",
        market_value: null,
        error_code,
      },
    ],
  });
  expect(wrapper.text()).toContain(label);
  wrapper.unmount();
});

it("shows CNY price and same-currency treatment without inventing an FX quote", () => {
  const wrapper = render({
    ...value,
    items: [
      {
        ...value.items[0]!,
        status: "current",
        quote: { ...value.items[0]!.quote!, currency: "CNY" },
        fx: null,
      },
    ],
  });
  expect(wrapper.text()).toContain("10.000001 CNY");
  expect(wrapper.text()).toContain("同币种，无需换汇");
  expect(wrapper.text()).toContain("当日参考");
  wrapper.unmount();
});
