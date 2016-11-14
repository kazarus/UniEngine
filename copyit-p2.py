#! /usr/bin/env python  
# -*- coding: utf-8 -*-  

   
import os  
import time
import shutil
   
sourceDir = r"C:\GOPATH\src\github.com\kazarus\UniEngine" 
targetDir = r"D:\GITHUB\UniEngine" 
fileCount = 0 
   
def copyFiles(sourceDir, targetDir):  
    global fileCount  
    print sourceDir  
    print u"%s 当前处理文件夹%s已处理%s 个文件" %(time.strftime('%Y-%m-%d %H:%M:%S',time.localtime(time.time())), sourceDir,fileCount)  
    for f in os.listdir(sourceDir):
        
        if f ==".git":
            continue
        
        sourceF = os.path.join(sourceDir, f)  
        targetF = os.path.join(targetDir, f)
        print sourceF
                 
        if os.path.isfile(sourceF):  
            #创建目录  
            if not os.path.exists(targetDir):  
                os.makedirs(targetDir)  
            fileCount += 1
            
        shutil.copy(sourceF,targetF)
           
        if os.path.isdir(sourceF):  
            copyFiles(sourceF, targetF)  
           
if __name__ == "__main__":  
    try:  
        import psyco  
        psyco.profile()  
    except ImportError:  
        pass 
    copyFiles(sourceDir,targetDir)
