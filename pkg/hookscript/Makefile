.PHONY: test test-php ext-package

test: test-php

test-php:
	../../vendor/bin/pest --configuration phpunit.xml.dist

ext-package:
	php scripts/build-ext-package "$(DEST)" "$(VERSION)" $(if $(REVISION),"$(REVISION)")
