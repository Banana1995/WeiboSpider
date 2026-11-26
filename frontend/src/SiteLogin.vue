<script setup lang="ts">
import { ref } from "vue";
import UserIdentity from "./UserIdentity.vue";
import {
  accountRequest,
  afterLogin,
  siteUser,
  notifySessionChange,
} from "./siteAccount";
const props = defineProps<{
  setupCode?: string;
  initialName?: string;
  account?: boolean;
}>();
const name = ref(props.initialName ?? ""),
  password = ref(""),
  confirm = ref(""),
  visible = ref(false),
  busy = ref(false),
  error = ref("");
async function submit() {
  if (busy.value) return;
  error.value = "";
  if (props.setupCode && password.value !== confirm.value) {
    error.value = "两次输入的密码不一致";
    return;
  }
  busy.value = true;
  try {
    if (props.account) {
      await accountRequest("logout", {});
      siteUser.value = undefined;
      notifySessionChange();
      location.replace("/login");
      return;
    }
    await accountRequest(props.setupCode ? "activate" : "login", {
      username: name.value,
      password: password.value,
      ...(props.setupCode ? { code: props.setupCode } : {}),
    });
    password.value = confirm.value = "";
    notifySessionChange();
    location.replace(
      location.pathname.replace(/\/$/, "") === "/login"
        ? afterLogin(location.search)
        : afterLogin(
            "?" +
              new URLSearchParams({
                next: location.pathname + location.search,
              }).toString(),
          ),
    );
  } catch (e) {
    error.value = e instanceof Error ? e.message : "连接失败，请重试";
  } finally {
    busy.value = false;
  }
}
</script>

<template>
  <main class="passport">
    <section
      class="passport-panel"
      :aria-label="account ? '我的账户' : '账户登录'"
    >
      <div class="passport-brand">
        <img src="/guanjia.svg" width="32" height="32" alt="" />观价
      </div>
      <h1>
        {{ account ? "我的账户" : setupCode ? "设置账户密码" : "登录观价" }}
      </h1>
      <p>
        {{
          account
            ? "你正在使用自己的工作空间。"
            : setupCode
              ? "为你的账户设置密码，以后使用用户名和密码登录。"
              : "登录后查看你的行情、投资账本和税务资料。"
        }}
      </p>
      <form @submit.prevent="submit">
        <template v-if="account"
          ><dl>
            <dt>当前账户</dt>
            <dd v-if="siteUser">
              <UserIdentity :username="siteUser.username" />
            </dd>
          </dl>
          <a class="passport-back" href="/">返回工作空间</a></template
        >
        <template v-else>
          <label for="site-name">用户名</label
          ><input
            id="site-name"
            v-model="name"
            autocomplete="username"
            autocapitalize="none"
            spellcheck="false"
            maxlength="32"
            :readonly="!!setupCode"
            required
          />
          <label for="site-password">{{ setupCode ? "新密码" : "密码" }}</label>
          <div class="passport-password">
            <input
              id="site-password"
              v-model="password"
              :type="visible ? 'text' : 'password'"
              :autocomplete="setupCode ? 'new-password' : 'current-password'"
              :minlength="setupCode ? 12 : undefined"
              required
            /><button
              type="button"
              :aria-pressed="visible"
              @click="visible = !visible"
            >
              {{ visible ? "隐藏" : "显示" }}
            </button>
          </div>
          <template v-if="setupCode"
            ><small>至少 12 个字符，可使用密码管理器生成。</small
            ><label for="site-confirm">确认新密码</label
            ><input
              id="site-confirm"
              v-model="confirm"
              type="password"
              autocomplete="new-password"
              required
          /></template>
        </template>
        <p v-if="error" role="alert" class="passport-error">{{ error }}</p>
        <button class="passport-submit" :disabled="busy">
          {{
            busy
              ? "请稍候…"
              : account
                ? "退出登录"
                : setupCode
                  ? "设置密码并登录"
                  : "登录"
          }}
        </button>
      </form>
      <p v-if="!account && !setupCode" class="passport-help">
        首次使用？请打开站主提供的密码设置链接。
      </p>
    </section>
  </main>
</template>
<style scoped>
.passport {
  min-height: 100svh;
  display: grid;
  place-items: center;
  padding: 28px 18px;
  background: #f2f5f7;
  color: #192e41;
  font-family: var(--workspace-font, system-ui);
}
.passport-panel {
  width: min(100%, 414px);
  padding: 36px clamp(22px, 5vw, 42px);
  background: white;
  border-top: 4px solid #305974;
  border-radius: 4px 4px 12px 12px;
  box-shadow: 0 12px 44px #29496314;
}
.passport-brand {
  display: flex;
  align-items: center;
  gap: 9px;
  font-size: 18px;
  font-weight: 650;
}
.passport h1 {
  margin: 30px 0 8px;
  font-size: 1.9rem;
  line-height: 1.25;
}
.passport p,
.passport small,
.passport dt {
  color: #506575;
  line-height: 1.6;
}
.passport p {
  font-size: 0.9rem;
  margin: 0 0 22px;
}
.passport form {
  display: grid;
  gap: 7px;
}
.passport label {
  margin-top: 9px;
  font-weight: 600;
  font-size: 0.9rem;
}
.passport input {
  width: 100%;
  min-width: 0;
  padding: 11px 13px;
  border: 1px solid #98aab5;
  border-radius: 5px;
  color: inherit;
  background: #fff;
}
.passport input:focus-visible,
.passport button:focus-visible,
.passport a:focus-visible {
  outline: 3px solid #6295ba;
  outline-offset: 2px;
}
.passport-password {
  position: relative;
}
.passport-password input {
  padding-right: 58px;
}
.passport-password button {
  position: absolute;
  inset: 2px 3px 2px auto;
  background: transparent;
  border: 0;
  color: #305974;
  padding: 0 10px;
}
.passport-submit {
  margin-top: 16px;
  padding: 12px;
  border: 0;
  border-radius: 5px;
  background: #305974;
  color: #fff;
  font-weight: 650;
}
.passport-submit:disabled {
  opacity: 0.65;
}
.passport .passport-error {
  color: #a32732;
  margin: 8px 0 0;
}
.passport .passport-help {
  margin: 22px 0 0;
  font-size: 0.8rem;
}
.passport dd {
  --avatar-size: 40px;
  margin: 6px 0 2px;
  font-size: 1.2rem;
  font-weight: 600;
}
.passport-back {
  text-decoration: underline;
}
</style>
