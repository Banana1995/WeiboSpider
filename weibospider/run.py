#!/usr/bin/env python
# encoding: utf-8
"""微博管理器启动入口。
python run.py              # 生产模式 (waitress)
python run.py --dev        # 开发模式 (Flask热更新，改代码自动生效)
python run.py --port 8080
"""
import argparse
import sys
import os

sys.path.insert(0, os.path.dirname(__file__))


def main():
    parser = argparse.ArgumentParser(description='微博管理器')
    parser.add_argument('--port', type=int, default=5000, help='Web 服务端口 (默认: 5000)')
    parser.add_argument('--host', default='0.0.0.0', help='监听地址 (默认: 0.0.0.0)')
    parser.add_argument('--dev', action='store_true', help='开发模式：改代码自动热更新')
    args = parser.parse_args()

    from app import create_app
    app = create_app()
    print(f"微博管理器已启动: http://localhost:{args.port}")

    if args.dev:
        os.environ['FLASK_ENV'] = 'development'
        app.run(host=args.host, port=args.port, debug=True, use_reloader=True, threaded=True)
    else:
        from waitress import serve
        serve(app, host=args.host, port=args.port, threads=8)


if __name__ == '__main__':
    main()
