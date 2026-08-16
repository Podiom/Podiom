require 'json'

package = JSON.parse(File.read(File.join(__dir__, 'package.json')))

# Capacitor 8 uses Swift Package Manager when every plugin ships a Package.swift
# (this one does). The podspec is the fallback for a project that has been
# switched back to CocoaPods.
Pod::Spec.new do |s|
  s.name = 'PodiomDiscovery'
  s.version = package['version']
  s.summary = package['description']
  s.license = 'MIT'
  s.homepage = 'https://github.com/Podiom/Podiom'
  s.author = 'Podiom'
  s.source = { :git => 'https://github.com/Podiom/Podiom.git', :tag => s.version.to_s }
  s.source_files = 'ios/Sources/**/*.{swift,h,m,c,cc,mm,cpp}'
  s.ios.deployment_target = '15.0'
  s.dependency 'Capacitor'
  s.swift_version = '5.9'
end
