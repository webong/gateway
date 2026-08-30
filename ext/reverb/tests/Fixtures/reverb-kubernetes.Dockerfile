FROM sail-8.4/app:latest

WORKDIR /workspace

COPY . /workspace
COPY --from=dependencies . /workspace/vendor

RUN mkdir -p /workspace/tests/Fixtures/octane-app/database

COPY --from=smoke reverb.sqlite /workspace/tests/Fixtures/octane-app/database/reverb.sqlite

WORKDIR /workspace/tests/Fixtures/octane-app
