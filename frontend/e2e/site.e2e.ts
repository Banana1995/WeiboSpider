import { test, expect } from "@playwright/test";
import { randomUUID } from "node:crypto";
test("private deep link, one-use password setup, logout and password login", async ({
  page,
  request,
}) => {
  await page.setViewportSize({ width: 390, height: 820 });
  await page.goto("/tax");
  await expect(page.getByRole("heading", { name: "登录观价" })).toBeVisible();
  expect((await page.request.get("/api/platform/tax/state")).status()).toBe(
    401,
  );
  const { link } = await (
    await request.post("http://127.0.0.1:17052/setup")
  ).json();
  const password = randomUUID();
  await page.goto(link);
  await expect(page.getByLabel("用户名")).toHaveValue("gaomanyi");
  await expect(page).toHaveURL(/\/login$/);
  await page.getByLabel("新密码", { exact: true }).fill(password);
  await page.getByLabel("确认新密码").fill(password + "x");
  await page.getByRole("button", { name: "设置密码并登录" }).click();
  await expect(page.getByRole("alert")).toContainText("不一致");
  await page.getByLabel("确认新密码").fill(password);
  await page.getByRole("button", { name: "设置密码并登录" }).click();
  await expect(page).toHaveURL(/\/$/);
  await page.goto("/account");
  await expect(page.getByText("gaomanyi", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "退出登录" }).click();
  await expect(page).toHaveURL(/\/login$/);
  expect((await page.request.get("/api/platform/tax/state")).status()).toBe(
    401,
  );
  await page.goto("/tax");
  await page.getByLabel("用户名").fill("gaomanyi");
  await page.getByLabel("密码", { exact: true }).fill(password + "x");
  await page.getByRole("button", { name: "登录", exact: true }).click();
  await expect(page.getByRole("alert")).toContainText("不正确");
  await page.getByLabel("密码", { exact: true }).fill(password);
  await page.getByRole("button", { name: "登录", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "税务计算", exact: true }),
  ).toBeVisible();
  expect((await page.request.get("/api/platform/tax/state")).status()).toBe(
    200,
  );
});
